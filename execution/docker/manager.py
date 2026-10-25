#!/usr/bin/env python3
"""Node-local trusted Docker manager. No AIM credentials, arbitrary code or TCP API."""
import argparse
import base64
import binascii
import copy
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import signal
import socket
import socketserver
import stat
import subprocess
import threading
import time
import uuid

ENTRYPOINT = ["python3", "-I", "-B", "/opt/aim/worker.py"]
DEFAULT_LIMITS = dict(cpu_millis=500, memory_bytes=134217728, pids=32,
                      workspace_bytes=8388608, tmp_bytes=2097152,
                      total_seconds=30, step_seconds=15, input_bytes=65536,
                      output_bytes=65536, output_files=1, log_bytes=65536)
RANGES = dict(cpu_millis=(100, 1000), memory_bytes=(67108864, 268435456),
              pids=(8, 64), workspace_bytes=(1048576, 16777216),
              tmp_bytes=(1048576, 8388608), total_seconds=(1, 60),
              step_seconds=(1, 30), input_bytes=(1, 65536),
              output_bytes=(1, 65536), output_files=(1, 4), log_bytes=(1024, 65536))
PROBES = frozenset("probe-security probe-resources probe-disk probe-log probe-process probe-memory probe-pids probe-failure probe-output probe-files".split())
MAX_REQUEST = 100000
MAX_RECORDS = 512
OWNER_LABEL = "aim.execution.owner"
ID_LABEL = "aim.execution.id"


class Rejected(ValueError):
    pass


def strict_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise Rejected("duplicate JSON key")
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=pairs,
                      parse_constant=lambda _: (_ for _ in ()).throw(Rejected("nonfinite JSON")))


def atomic_json(path, value):
    data = json.dumps(value, separators=(",", ":"), allow_nan=False).encode()
    if len(data) > 1048576:
        raise Rejected("durable execution record exceeds 1MiB budget")
    temporary = path.with_suffix(".new")
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
    fd = os.open(path.parent, os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


class Manager:
    def __init__(self, state_dir, policy_path, docker_socket="/var/run/docker.sock"):
        self.lock = threading.RLock()
        self.halt = threading.Event()
        self.active = {}
        self.records = {}
        self.closing = False
        self.state = Path(state_dir).absolute()
        self.state.mkdir(mode=0o700, parents=True, exist_ok=True)
        st = self.state.lstat()
        if not stat.S_ISDIR(st.st_mode) or st.st_uid != os.geteuid() or st.st_mode & 0o077:
            raise Rejected("state directory must be owned by manager and mode 0700")
        self.lock_fd = os.open(self.state / "lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        fcntl.flock(self.lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        policy_path = Path(policy_path)
        st = policy_path.lstat()
        if not stat.S_ISREG(st.st_mode) or st.st_uid != 0 or st.st_mode & 0o022:
            raise Rejected("execution policy must be root-owned and not group/world writable")
        self.policy = strict_json(policy_path.read_bytes())
        required = {"schema", "image_id", "worker_sha256", "reviewed_by", "reviewed_at", "entrypoint"}
        if set(self.policy) != required or self.policy["schema"] != 1:
            raise Rejected("invalid execution policy")
        if (not re.fullmatch(r"sha256:[0-9a-f]{64}", self.policy["image_id"]) or
                not re.fullmatch(r"[0-9a-f]{64}", self.policy["worker_sha256"]) or
                self.policy["entrypoint"] != ENTRYPOINT or
                not isinstance(self.policy["reviewed_by"], str) or not self.policy["reviewed_by"].strip() or
                not isinstance(self.policy["reviewed_at"], str) or not self.policy["reviewed_at"].strip()):
            raise Rejected("policy requires frozen identity and explicit human execution review")
        socket_path = Path(docker_socket)
        socket_stat = socket_path.lstat()
        if not socket_path.is_absolute() or not stat.S_ISSOCK(socket_stat.st_mode) or socket_stat.st_uid != 0:
            raise Rejected("requires a root-owned local Unix Docker socket")
        self.env = {"PATH": "/usr/bin:/bin", "HOME": "/nonexistent", "DOCKER_HOST": "unix://" + docker_socket,
                    "DOCKER_CONFIG": "/nonexistent", "LANG": "C.UTF-8"}
        owner_path = self.state / "owner.json"
        if owner_path.exists():
            self.owner = strict_json(owner_path.read_bytes())["owner"]
            uuid.UUID(self.owner)
        else:
            self.owner = str(uuid.uuid4())
            atomic_json(owner_path, {"owner": self.owner})
        for path in self.state.glob("*.json"):
            if path.name == "owner.json":
                continue
            if path.is_symlink() or path.stat().st_size > 1048576:
                raise Rejected("invalid execution journal")
            record = strict_json(path.read_bytes())
            if path.stem != record["execution_id"] or record["owner"] != self.owner:
                raise Rejected("journal ownership mismatch")
            uuid.UUID(path.stem)
            if record.get("validation"):
                self._save(record)  # Remove raw outputs from pre-retention journals before serving reads.
            self.records[path.stem] = record
        self.host = self._preflight()
        self.recover()
        self.reaper = threading.Thread(target=self._reaper, name="execution-recovery", daemon=True)
        self.reaper.start()

    def _docker(self, *args, timeout=5, check=True):
        # No caller-provided flags, shell, Docker context, configuration or environment.
        process = subprocess.Popen(["/usr/bin/docker", *args], env=self.env,
                                   stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        selector = selectors.DefaultSelector()
        selector.register(process.stdout, selectors.EVENT_READ, "out")
        selector.register(process.stderr, selectors.EVENT_READ, "err")
        output = {"out": bytearray(), "err": bytearray()}
        deadline = time.monotonic() + timeout
        try:
            while selector.get_map():
                if time.monotonic() >= deadline:
                    raise TimeoutError("Docker operation outcome unknown")
                for key, _ in selector.select(0.1):
                    chunk = os.read(key.fileobj.fileno(), 8192)
                    if not chunk:
                        selector.unregister(key.fileobj)
                        continue
                    output[key.data].extend(chunk)
                    if sum(map(len, output.values())) > 2097152:
                        raise Rejected("Docker response exceeded management budget")
            code = process.wait(timeout=max(0.1, deadline - time.monotonic()))
            if code and check:
                raise RuntimeError(bytes(output["err"]).decode(errors="replace")[:2048])
            return code, bytes(output["out"]), bytes(output["err"])
        finally:
            selector.close()
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdout.close()
            process.stderr.close()

    def _inspect(self, identity):
        code, out, err = self._docker("container", "inspect", identity, check=False)
        if code:
            # A daemon/network error must never masquerade as object absence.
            if b"No such container:" in err or b"No such object:" in err:
                return None
            raise RuntimeError(err.decode(errors="replace")[:2048])
        return strict_json(out)[0]

    def _preflight(self):
        _, out, _ = self._docker("info", "--format", "{{json .}}")
        info = strict_json(out)
        security = " ".join(info.get("SecurityOptions", []))
        if (info.get("OSType") != "linux" or info.get("CgroupVersion") != "2" or
                info.get("CgroupDriver") != "systemd" or "seccomp" not in security or
                "rootless" in security or "userns" in security):
            raise Rejected("requires qualified local rootful Linux cgroup2/systemd with default seccomp; rootless/userns unqualified")
        controllers = Path("/sys/fs/cgroup/cgroup.controllers").read_text().split()
        if not {"cpu", "memory", "pids"}.issubset(controllers):
            raise Rejected("required cgroup controllers unavailable")
        root = Path(info["DockerRootDir"])
        if not root.is_absolute() or root.is_symlink() or not os.access(root, os.R_OK | os.X_OK):
            raise Rejected("manager needs local Docker resource observation; run on execution node with required permissions")
        if Path("/sys/module/apparmor/parameters/enabled").exists():
            if Path("/sys/module/apparmor/parameters/enabled").read_text().strip() == "Y" and "apparmor" not in security:
                raise Rejected("host AppArmor enabled but Docker has no AppArmor support")
        if Path("/sys/fs/selinux/enforce").exists() and "selinux" not in security:
            raise Rejected("host SELinux available but Docker lacks SELinux support")
        _, raw, _ = self._docker("image", "inspect", self.policy["image_id"])
        image = strict_json(raw)[0]
        config = image["Config"]
        if (image["Id"] != self.policy["image_id"] or config.get("Entrypoint") != ENTRYPOINT or
                config.get("User") != "65532:65532" or config.get("Volumes") or config.get("Cmd") or
                config.get("Healthcheck") or config.get("ExposedPorts") or
                config.get("Labels", {}).get("aim.worker.sha256") != self.policy["worker_sha256"]):
            raise Rejected("frozen image configuration differs from reviewed execution identity")
        # Label alone cannot attest script content: inspect the reviewed local build context too.
        worker = Path(__file__).with_name("worker.py").read_bytes()
        if hashlib.sha256(worker).hexdigest() != self.policy["worker_sha256"]:
            raise Rejected("local worker bytes differ from reviewed script")
        _, version, _ = self._docker("version", "--format", "{{json .Server}}")
        return {"engine": strict_json(version), "kernel": info["KernelVersion"], "daemon_id": info["ID"],
                "docker_root": str(root), "storage_driver": info["Driver"], "security_options": info["SecurityOptions"],
                "cgroup_driver": info["CgroupDriver"], "cgroup_version": info["CgroupVersion"],
                "rootless_userns": "not qualified; rejected", "daemon_persisted_log_bytes": 0}

    def _save(self, record):
        if record.get("validation"):
            record["result"] = self._validation_result(record.get("result"))
            record.get("evidence", {}).pop("stderr", None)
        atomic_json(self.state / (record["execution_id"] + ".json"), record)

    def get(self, execution_id):
        with self.lock:
            if execution_id not in self.records:
                raise Rejected("unknown execution")
            return copy.deepcopy(self.records[execution_id])

    def _validation_result(self, result):
        if result is None:
            return None
        files = []
        for item in result.get("files", []):
            if "data_b64" in item:
                data = base64.b64decode(item["data_b64"], validate=True)
                files.append(dict(name=item["name"], sha256=hashlib.sha256(data).hexdigest(), size=len(data)))
            else:
                files.append({key: item[key] for key in ("name", "sha256", "size")})
        return dict(files=files)

    def validation_review(self, identity):
        if (not isinstance(identity, dict) or set(identity) != {"scripts", "image_digest", "entry_point"} or
                not isinstance(identity["scripts"], dict) or set(identity["scripts"]) != {"worker.py"} or
                not isinstance(identity["scripts"]["worker.py"], str) or
                len(identity["scripts"]["worker.py"].encode("utf-8")) > 65536):
            raise Rejected("only exact reviewed worker.py is admitted")
        expected = dict(worker_sha256=hashlib.sha256(identity["scripts"]["worker.py"].encode("utf-8")).hexdigest(),
                        image_id=identity["image_digest"], entry_point=identity["entry_point"])
        return self._validation_admission(expected)

    def _validation_admission(self, expected):
        if (not isinstance(expected, dict) or set(expected) != {"worker_sha256", "image_id", "entry_point"} or
                expected["worker_sha256"] != self.policy["worker_sha256"] or
                expected["image_id"] != self.policy["image_id"] or
                expected["entry_point"] != " ".join(ENTRYPOINT)):
            raise Rejected("candidate differs from root-installed human execution policy")
        host = self._preflight()  # Recheck local worker, pinned image and host at each admission.
        frozen = dict(schema=1, capability="csv-v1", **expected)
        digest = hashlib.sha256(json.dumps(frozen, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        environment = dict(schema=1, host=host, identity=frozen, limits=DEFAULT_LIMITS,
                           network="none", user="65532:65532", read_only=True, log_driver="none")
        environment_digest = hashlib.sha256(json.dumps(environment, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        return dict(review_digest=digest, environment_digest=environment_digest, **expected)

    def validation_submit(self, request):
        if not isinstance(request, dict) or set(request) != {"execution_key", "identity", "input_b64", "expected_sha256"}:
            raise Rejected("invalid validation submission")
        key = request["execution_key"]
        expected_sha = request["expected_sha256"]
        if (not isinstance(key, str) or not re.fullmatch(r"[0-9a-f]{64}", key) or
                not isinstance(expected_sha, str) or not re.fullmatch(r"[0-9a-f]{64}", expected_sha)):
            raise Rejected("invalid validation identity")
        admission = self._validation_admission(request["identity"])
        validation = dict(review_digest=admission["review_digest"], environment_digest=admission["environment_digest"],
                          expected_sha256=expected_sha, identity=copy.deepcopy(request["identity"]))
        fixed = dict(run_id=key, generation=1, capability="csv-v1", input_b64=request["input_b64"],
                     limits=copy.deepcopy(DEFAULT_LIMITS))
        return self.validation_view(self.submit(fixed, validation=validation))

    def validation_view(self, record):
        if not record.get("validation"):
            raise Rejected("not a validation execution")
        evidence = record.get("evidence", {})
        result = record.get("result") or {}
        failure_class = record.get("failure_class", "environment_error")
        if record["outcome"] == "succeeded":
            failure_class = ""
        elif failure_class == "worker_failure":
            if (not evidence.get("input_delivered") or
                    record["outcome"] not in ("failed", "timeout", "output_limit") or
                    (record["outcome"] == "timeout" and evidence.get("workload_timeout") is not True) or
                    (record["outcome"] == "failed" and
                     ("attached_exit_code" not in evidence or evidence.get("wait") != evidence["attached_exit_code"]))):
                failure_class = "environment_error"
        return {key: copy.deepcopy(record[key]) for key in
                ("execution_id", "run_id", "phase", "outcome", "input_sha256", "image_id", "worker_sha256", "validation")} | dict(
                    container_id=record.get("container_id"), files=copy.deepcopy(result.get("files", [])),
                    removed=evidence.get("removed") is True, resources_absent=evidence.get("resources_absent") is True,
                    stop_confirmed=evidence.get("stop_confirmed") is True, wait=evidence.get("wait"),
                    failure_class=failure_class)

    def validation_lookup(self, key):
        if not isinstance(key, str) or not re.fullmatch(r"[0-9a-f]{64}", key):
            raise Rejected("invalid validation lookup key")
        with self.lock:
            for record in self.records.values():
                if record["run_id"] == key and record["generation"] == 1:
                    return self.validation_view(record)
        return None

    def submit(self, request, probe=False, validation=None):
        if not isinstance(request, dict) or set(request) != {"run_id", "generation", "capability", "input_b64", "limits"}:
            raise Rejected("unknown or missing request fields")
        run_id = request["run_id"]
        generation = request["generation"]
        operation = request["capability"]
        if not isinstance(run_id, str) or not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", run_id):
            raise Rejected("invalid Run identity")
        if type(generation) is not int or not 1 <= generation <= 9007199254740991:
            raise Rejected("invalid execution generation")
        if operation != "csv-v1" and not (probe and operation in PROBES):
            raise Rejected("only reviewed fixed csv-v1 capability is callable")
        limits = request["limits"]
        if not isinstance(limits, dict) or set(limits) != set(RANGES):
            raise Rejected("all finite limits are required")
        for key, (low, high) in RANGES.items():
            if type(limits[key]) is not int or not low <= limits[key] <= high:
                raise Rejected("invalid finite limit: " + key)
        if limits["step_seconds"] > limits["total_seconds"]:
            raise Rejected("step deadline exceeds total deadline")
        encoded = request["input_b64"]
        if not isinstance(encoded, str) or len(encoded) > 4 * ((limits["input_bytes"] + 2) // 3):
            raise Rejected("input byte budget exceeded")
        try:
            data = base64.b64decode(request["input_b64"], validate=True)
        except (ValueError, TypeError, binascii.Error):
            raise Rejected("invalid input encoding") from None
        if len(data) > limits["input_bytes"]:
            raise Rejected("input byte budget exceeded")
        with self.lock:
            if self.closing:
                raise Rejected("manager draining")
            if any(r["run_id"] == run_id and r["generation"] == generation for r in self.records.values()):
                raise Rejected("Run generation already recorded; query it, never replay")
            if len(self.records) >= MAX_RECORDS:
                raise Rejected("journal capacity exhausted; operator archival required")
            if sum(r["phase"] != "cleaned" for r in self.records.values()) >= 2:
                raise Rejected("execution concurrency budget exhausted; no queue")
            execution_id = str(uuid.uuid4())
            now = time.time()
            record = dict(execution_id=execution_id, owner=self.owner, run_id=run_id, generation=generation,
                          phase="intent", outcome="unknown", container_id=None,
                          container_name="aim-exec-" + execution_id, input_sha256=hashlib.sha256(data).hexdigest(),
                          image_id=self.policy["image_id"], worker_sha256=self.policy["worker_sha256"],
                          created_at=now, deadline=now + limits["total_seconds"], limits=copy.deepcopy(limits),
                          operation=operation, evidence={"host": self.host}, result=None, error="", stop_requested=False)
            if validation is not None:
                record["validation"] = copy.deepcopy(validation)
            self.records[execution_id] = record
            self._save(record)  # Intent and exact owner/name precede Docker create, including unknown outcomes.
            worker = threading.Thread(target=self._execute, args=(record, request["input_b64"]), daemon=True)
            self.active[execution_id] = worker
            worker.start()
            return copy.deepcopy(record)

    def _owned(self, record, inspect):
        labels = inspect["Config"].get("Labels", {})
        if (labels.get(OWNER_LABEL) != self.owner or labels.get(ID_LABEL) != record["execution_id"] or
                inspect["Name"] != "/" + record["container_name"] or
                record.get("container_id") not in (None, inspect["Id"])):
            raise Rejected("container ownership mismatch; no destructive operation permitted")

    def _paths(self, inspect):
        root = Path(self.host["docker_root"]).resolve()
        directory = root / "containers" / inspect["Id"]
        if directory.resolve() != directory or not directory.is_dir():
            raise Rejected("cannot observe exact local container resources")
        return str(directory)

    def _running_evidence(self, record, inspect):
        self._owned(record, inspect)
        config, host = inspect["Config"], inspect["HostConfig"]
        limits = record["limits"]
        if (config["User"] != "65532:65532" or config["Entrypoint"] != ENTRYPOINT or config.get("Volumes") or
                not host["ReadonlyRootfs"] or host["Privileged"] or host["NetworkMode"] != "none" or
                host.get("Binds") or host.get("Devices") or host.get("DeviceRequests") or host.get("Mounts") or
                host.get("PidMode") or host["IpcMode"] != "none" or host.get("PortBindings") or
                host.get("LogConfig", {}).get("Type") != "none" or host.get("CapAdd") or
                host.get("CapDrop") != ["ALL"] or "no-new-privileges" not in host["SecurityOpt"] or
                any("unconfined" in option for option in host["SecurityOpt"])):
            raise Rejected("actual container isolation configuration mismatch")
        pid = inspect["State"]["Pid"]
        proc = Path("/proc") / str(pid)
        status = proc.joinpath("status").read_text()
        facts = dict(line.split(":", 1) for line in status.splitlines() if ":" in line)
        if (facts["Uid"].split() != ["65532"] * 4 or facts["Gid"].split() != ["65532"] * 4 or
                int(facts["CapEff"].strip(), 16) or facts["NoNewPrivs"].strip() != "1" or facts["Seccomp"].strip() != "2"):
            raise Rejected("runtime UID/GID/capabilities/NNP/seccomp mismatch")
        if "apparmor" in " ".join(self.host["security_options"]):
            if "docker-default" not in proc.joinpath("attr/current").read_text():
                raise Rejected("actual AppArmor profile missing")
        if "selinux" in " ".join(self.host["security_options"]):
            if "container_t" not in inspect.get("ProcessLabel", ""):
                raise Rejected("actual SELinux container label missing")
        cgroup = proc.joinpath("cgroup").read_text().strip()
        if not cgroup.startswith("0::/") or ".." in cgroup.split("/"):
            raise Rejected("unsupported cgroup path")
        path = Path("/sys/fs/cgroup") / cgroup[4:]
        values = {name: path.joinpath(name).read_text().strip() for name in
                  ("cpu.max", "memory.max", "memory.swap.max", "pids.max", "memory.current", "cgroup.procs")}
        quota, period = values["cpu.max"].split()
        if (quota == "max" or int(quota) * 1000 != limits["cpu_millis"] * int(period) or
                values["memory.max"] != str(limits["memory_bytes"]) or values["memory.swap.max"] != "0" or
                values["pids.max"] != str(limits["pids"])):
            raise Rejected("runtime hard cgroup limits unavailable; input not delivered")
        mountinfo = proc.joinpath("mountinfo").read_text()
        mounts = [line.split() for line in mountinfo.splitlines()]
        for target in ("/", "/opt/aim"):
            covering = [m for m in mounts if target == m[4] or target.startswith(m[4].rstrip("/") + "/")]
            mount = max(covering, key=lambda m: len(m[4]))
            if "ro" not in mount[5].split(","):
                raise Rejected("root/package mount not actually read-only")
        for target, size in (("/workspace", limits["workspace_bytes"]), ("/tmp", limits["tmp_bytes"]), ("/dev/shm", 1048576)):
            entry = next((m for m in mounts if m[4] == target), None)
            if entry is None or "tmpfs" not in entry:
                raise Rejected("missing bounded tmpfs")
            actual = os.statvfs(proc / "root" / target.lstrip("/"))
            if actual.f_blocks * actual.f_frsize > size:
                raise Rejected("tmpfs hard capacity mismatch")
        record["evidence"].update(inspect=inspect, proc_status=status, mountinfo=mountinfo,
                                    cgroup={"path": str(path), "values": values},
                                    resource_directory=self._paths(inspect),
                                    host_pids=[{"pid": pid, "starttime": proc.joinpath("stat").read_text().rsplit(")", 1)[1].split()[19]}])
        self._save(record)

    def _execution_timeout(self, record, workload_deadline):
        record["outcome"] = "timeout"
        record["failure_class"] = "environment_error"
        # An expired Docker CLI/pipe budget is not proof that the workload timed out.
        # Require a full step budget after input delivery and a fresh owned running observation.
        if workload_deadline is None or time.monotonic() < workload_deadline:
            return
        current = self._inspect(record["container_id"])
        if current is None:
            return
        self._owned(record, current)
        if current["State"]["Running"]:
            record["evidence"]["workload_timeout"] = True
            record["failure_class"] = "worker_failure"

    def _execute(self, record, input_b64):
        process = None
        total_deadline = time.monotonic() + max(0, record["deadline"] - time.time())
        try:
            limits = record["limits"]
            with self.lock:
                if record["stop_requested"] or time.monotonic() >= total_deadline:
                    record["phase"] = "cleaned"
                    record["outcome"] = "stopped" if record["stop_requested"] else "timeout"
                    record["evidence"].update(no_create_attempted=True, removed=True, resources_absent=True)
                    self._save(record)
                    return
            args = ["create", "--pull=never", "--name", record["container_name"],
                    "--label", OWNER_LABEL + "=" + self.owner, "--label", ID_LABEL + "=" + record["execution_id"],
                    "--user", "65532:65532", "--read-only", "--cap-drop", "ALL",
                    "--security-opt", "no-new-privileges", "--network", "none", "--ipc", "none",
                    "--cgroupns", "private", "--cpus", str(limits["cpu_millis"] / 1000),
                    "--memory", str(limits["memory_bytes"]), "--memory-swap", str(limits["memory_bytes"]),
                    "--pids-limit", str(limits["pids"]), "--log-driver", "none",
                    "--ulimit", "nofile=128:128", "--ulimit", "core=0:0", "--ulimit", "msgqueue=8192:8192",
                    "--tmpfs", f"/workspace:rw,noexec,nosuid,nodev,size={limits['workspace_bytes']},mode=700,uid=65532,gid=65532,nr_inodes=64",
                    "--tmpfs", f"/tmp:rw,noexec,nosuid,nodev,size={limits['tmp_bytes']},mode=700,uid=65532,gid=65532,nr_inodes=64",
                    "--tmpfs", "/dev/shm:rw,noexec,nosuid,nodev,size=1048576,mode=700,uid=65532,gid=65532,nr_inodes=64",
                    "--interactive", self.policy["image_id"]]
            _, raw, _ = self._docker(*args)
            identity = raw.decode().strip()
            if not re.fullmatch(r"[0-9a-f]{64}", identity):
                raise Rejected("create identity unknown")
            with self.lock:
                record["container_id"] = identity
                record["phase"] = "created"
                self._save(record)
                if record["stop_requested"] or time.time() >= record["deadline"]:
                    record["outcome"] = "stopped" if record["stop_requested"] else "timeout"
                    return
            process = subprocess.Popen(["/usr/bin/docker", "start", "--attach", "--interactive", identity],
                                       env=self.env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            running = None
            startup_deadline = min(total_deadline, time.monotonic() + 5)
            while time.monotonic() < startup_deadline:
                running = self._inspect(identity)
                if running and running["State"]["Running"]:
                    break
                if process.poll() is not None or self.halt.wait(0.05):
                    break
            if not running or not running["State"]["Running"]:
                raise Rejected("container never reached observed running state")
            with self.lock:
                self._running_evidence(record, running)
                if record["stop_requested"] or time.time() >= record["deadline"]:
                    raise Rejected("stop requested before input delivery")
                record["phase"] = "running"
                self._save(record)
            payload = dict(operation="csv" if record["operation"] == "csv-v1" else record["operation"],
                           input_b64=input_b64, step_seconds=limits["step_seconds"],
                           output_bytes=limits["output_bytes"], output_files=limits["output_files"])
            pending_input = memoryview(json.dumps(payload).encode() + b"\n")
            os.set_blocking(process.stdin.fileno(), False)
            deadline = min(total_deadline, time.monotonic() + limits["step_seconds"])
            workload_deadline = None
            selector = selectors.DefaultSelector()
            selector.register(process.stdout, selectors.EVENT_READ, "out")
            selector.register(process.stderr, selectors.EVENT_READ, "err")
            selector.register(process.stdin, selectors.EVENT_WRITE, "in")
            captured = {"out": bytearray(), "err": bytearray()}
            try:
                while selector.get_map():
                    if time.monotonic() >= deadline:
                        self._execution_timeout(record, workload_deadline)
                        break
                    if record["stop_requested"] or self.halt.is_set():
                        record["outcome"] = "stopped"
                        break
                    for key, _ in selector.select(0.05):
                        if key.data == "in":
                            try:
                                sent = os.write(process.stdin.fileno(), pending_input[:4096])
                            except BlockingIOError:
                                continue
                            pending_input = pending_input[sent:]
                            if not pending_input:
                                record["evidence"]["input_delivered"] = True
                                workload_deadline = time.monotonic() + limits["step_seconds"]
                                deadline = min(total_deadline, workload_deadline)
                                selector.unregister(process.stdin)
                                process.stdin.close()
                            continue
                        chunk = os.read(key.fileobj.fileno(), 4096)
                        if not chunk:
                            selector.unregister(key.fileobj)
                            continue
                        if sum(map(len, captured.values())) + len(chunk) > limits["log_bytes"]:
                            record["outcome"] = "output_limit"
                            if record["evidence"].get("input_delivered"):
                                record["failure_class"] = "worker_failure"
                            break
                        captured[key.data].extend(chunk)
                    if record["outcome"] == "output_limit":
                        break
                if record["outcome"] == "unknown":
                    try:
                        code = process.wait(timeout=max(0.1, deadline - time.monotonic()))
                    except subprocess.TimeoutExpired:
                        self._execution_timeout(record, workload_deadline)
                        raise
                    record["outcome"] = "succeeded" if code == 0 else "failed"
                    record["evidence"]["attached_exit_code"] = code
                    if code != 0 and record["evidence"].get("input_delivered"):
                        record["failure_class"] = "worker_failure"
                if record["outcome"] == "succeeded" and not captured["out"]:
                    if record["evidence"].get("input_delivered"):
                        record["failure_class"] = "worker_failure"
                    raise Rejected("fixed workload returned no result")
                record["evidence"]["attached_bytes"] = sum(map(len, captured.values()))
                if not record.get("validation"):
                    record["evidence"]["stderr"] = captured["err"].decode(errors="replace")
                if captured["out"]:
                    try:
                        result = strict_json(captured["out"])
                        self._validate_result(record, result)
                    except (ValueError, TypeError, KeyError):
                        if record["evidence"].get("input_delivered"):
                            record["failure_class"] = "worker_failure"
                        raise
                    record["result"] = self._validation_result(result) if record.get("validation") else result
            finally:
                selector.close()
        except Exception as exc:
            with self.lock:
                record["error"] = str(exc)[:2048]
                if record["outcome"] in ("unknown", "succeeded"):
                    record["outcome"] = "failed"
        finally:
            try:
                with self.lock:
                    self._recovery_checkpoint(record)
                self._cleanup(record)
            finally:
                try:
                    if process is not None:
                        if process.poll() is None:
                            process.kill()  # CLI only; never workload stop evidence.
                        process.wait()
                        for stream in (process.stdin, process.stdout, process.stderr):
                            if stream and not stream.closed:
                                stream.close()
                finally:
                    with self.lock:
                        self.active.pop(record["execution_id"], None)

    def _recovery_checkpoint(self, record):
        try:
            self._save(record)
        except (OSError, ValueError) as exc:
            self.closing = True  # Reject new work while durable state is unavailable.
            record["phase"] = "unknown"
            record["error"] = "journal unavailable; recovery retained: " + str(exc)[:1024]

    def lookup(self, run_id, generation):
        with self.lock:
            for record in self.records.values():
                if record["run_id"] == run_id and record["generation"] == generation:
                    return copy.deepcopy(record)
        raise Rejected("Run generation not recorded")

    def _validate_result(self, record, result):
        if not isinstance(result, dict) or set(result) != {"files", "facts"} or not isinstance(result["files"], list) or not isinstance(result["facts"], dict):
            raise Rejected("invalid fixed workload result")
        limits = record["limits"]
        files = result["files"]
        if len(files) > limits["output_files"]:
            record["outcome"] = "output_limit"
            raise Rejected("output file count budget exceeded")
        size = 0
        names = set()
        for item in files:
            if not isinstance(item, dict) or set(item) != {"name", "data_b64"} or item["name"] != "result.csv" or item["name"] in names:
                raise Rejected("only single fixed regular CSV output is allowed")
            names.add(item["name"])
            size += len(base64.b64decode(item["data_b64"], validate=True))
        if size > limits["output_bytes"]:
            record["outcome"] = "output_limit"
            raise Rejected("output byte budget exceeded")
        if record["operation"] == "csv-v1" and record["outcome"] == "succeeded" and len(files) != 1:
            raise Rejected("missing CSV result")
        record["evidence"]["output_bytes"] = size

    def _cleanup(self, record):
        # Never tied to the requesting socket, worker stdin or task cancellation.
        if record["evidence"].get("no_create_attempted"):
            return
        with self.lock:
            try:
                current = self._inspect(record.get("container_id") or record["container_name"])
                if current is None:
                    if record["evidence"].get("stop_confirmed") and record["evidence"].get("resource_directory"):
                        self._confirm_resources(record)
                        record["error"] = ""
                        record["phase"] = "cleaned"
                        self._recovery_checkpoint(record)
                        return
                    raise Rejected("container absent without prior stop/resource proof; requires operator reconciliation")
                self._owned(record, current)
                record["container_id"] = current["Id"]
                record["evidence"].setdefault("resource_directory", self._paths(current))
                record["phase"] = "stopping"
                self._recovery_checkpoint(record)
                if current["State"]["Running"]:
                    self._docker("stop", "--time", "1", current["Id"], timeout=3, check=False)
                    current = self._inspect(current["Id"])
                    if current is None:
                        raise Rejected("container disappeared during stop")
                    if current["State"]["Running"]:
                        self._docker("kill", "--signal", "KILL", current["Id"])
                never_started = current["State"]["Status"] == "created" and current["State"]["Pid"] == 0
                if never_started:
                    # Docker Wait blocks forever for a container that has never started.
                    wait = None
                else:
                    _, wait, _ = self._docker("wait", current["Id"])
                stopped = self._inspect(current["Id"])
                if stopped is None or stopped["State"]["Running"] or stopped["State"]["Pid"] != 0:
                    raise Rejected("whole container stop not confirmed")
                self._owned(record, stopped)
                self._confirm_processes(record)
                record["evidence"].update(wait=None if never_started else int(wait.strip()),
                                            never_started=never_started, stop_snapshot=stopped,
                                            stop_confirmed=True, removed=False)
                self._save(record)  # A crash after rm still retains stop and local resource identities.
                self._docker("rm", current["Id"])
                if self._inspect(current["Id"]) is not None:
                    raise Rejected("container removal unconfirmed")
                self._confirm_resources(record)
                record["error"] = ""
                record["phase"] = "cleaned"
            except Exception as exc:
                failures = record.setdefault("recovery_errors", [])
                if len(failures) < 16:
                    failures.append(str(exc)[:1024])
                record["phase"] = "unknown"
                record["error"] = str(exc)[:2048]
            self._recovery_checkpoint(record)

    def _confirm_processes(self, record):
        evidence = record["evidence"]
        cgroup = evidence.get("cgroup", {}).get("path")
        if cgroup and Path(cgroup).exists():
            if Path(cgroup, "cgroup.procs").read_text().strip():
                raise Rejected("container cgroup still contains processes")
            for child in Path(cgroup).rglob("cgroup.procs"):
                if child.read_text().strip():
                    raise Rejected("descendant cgroup still contains processes")
        for process in evidence.get("host_pids", []):
            path = Path("/proc") / str(process["pid"]) / "stat"
            try:
                current = path.read_text().rsplit(")", 1)[1].split()[19]
            except FileNotFoundError:
                continue
            if current == process["starttime"]:
                raise Rejected("recorded host process still present")

    def _confirm_resources(self, record):
        self._confirm_processes(record)
        directory = Path(record["evidence"]["resource_directory"])
        expected = Path(self.host["docker_root"]) / "containers" / record["container_id"]
        if directory != expected or directory.exists():
            raise Rejected("exact local Docker resource directory still present or mismatched")
        cgroup = record["evidence"].get("cgroup", {}).get("path")
        if cgroup and Path(cgroup).exists():
            raise Rejected("exact container cgroup still present")
        identity = record["container_id"]
        record["evidence"].update(removed=True, resources_absent=True)

    def stop(self, execution_id):
        with self.lock:
            record = self.records.get(execution_id)
            if record is None:
                raise Rejected("unknown execution")
            if record["phase"] == "cleaned":
                return copy.deepcopy(record)
            record["phase"] = "stopping"
            record["stop_requested"] = True
            record["outcome"] = "stopped"
            self._recovery_checkpoint(record)
            active = execution_id in self.active
        if not active:
            self._cleanup(record)
        return self.get(execution_id)

    def recover(self):
        with self.lock:
            pending = [r for key, r in self.records.items() if r["phase"] != "cleaned" and key not in self.active]
        for record in pending:
            self._cleanup(record)

    def _reaper(self):
        while not self.halt.wait(2):
            self.recover()

    def close(self):
        with self.lock:
            self.closing = True
            for record in self.records.values():
                if record["phase"] != "cleaned":
                    record["phase"] = "stopping"
                    record["stop_requested"] = True
                    record["outcome"] = "stopped"
                    self._recovery_checkpoint(record)
        self.halt.set()
        for worker in list(self.active.values()):
            worker.join(timeout=20)
        self.reaper.join(timeout=20)
        self.recover()
        if self.active:
            raise RuntimeError("execution workers not drained; recovery journal retained")
        fcntl.flock(self.lock_fd, fcntl.LOCK_UN)
        os.close(self.lock_fd)


class Server(socketserver.UnixStreamServer):
    allow_reuse_address = False
    request_queue_size = 4

    def __init__(self, address, manager, caller_uid):
        self.manager = manager
        self.caller_uid = caller_uid
        super().__init__(address, Handler)


class Handler(socketserver.StreamRequestHandler):
    def handle(self):
        self.connection.settimeout(2)
        import struct
        _, uid, _ = struct.unpack("3i", self.connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if uid not in (0, self.server.caller_uid):
            return
        try:
            raw = self.rfile.readline(MAX_REQUEST + 1)
            if len(raw) > MAX_REQUEST or not raw.endswith(b"\n"):
                raise Rejected("request exceeds finite message budget")
            request = strict_json(raw)
            action = request.get("action")
            if action == "submit" and set(request) == {"action", "request"}:
                result = self.server.manager.submit(request["request"])
            elif action in ("get", "stop") and set(request) == {"action", "execution_id"}:
                result = getattr(self.server.manager, action)(request["execution_id"])
            elif action == "lookup" and set(request) == {"action", "run_id", "generation"}:
                result = self.server.manager.lookup(request["run_id"], request["generation"])
            elif action == "validation-review" and set(request) == {"action", "identity"}:
                result = self.server.manager.validation_review(request["identity"])
            elif action == "validation-submit" and set(request) == {"action", "request"}:
                result = self.server.manager.validation_submit(request["request"])
            elif action == "validation-lookup" and set(request) == {"action", "execution_key"}:
                result = self.server.manager.validation_lookup(request["execution_key"])
            elif action in ("validation-get", "validation-stop") and set(request) == {"action", "execution_id"}:
                record = self.server.manager.get(request["execution_id"])
                self.server.manager.validation_view(record)  # Reject other execution classes before stop.
                if action == "validation-stop":
                    record = self.server.manager.stop(request["execution_id"])
                result = self.server.manager.validation_view(record)
            else:
                raise Rejected("unsupported narrow execution command")
            response = dict(ok=True, result=result)
        except Exception as exc:
            response = dict(ok=False, error=str(exc)[:2048])
        self.wfile.write(json.dumps(response, separators=(",", ":")).encode() + b"\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state", required=True)
    parser.add_argument("--policy", required=True)
    parser.add_argument("--socket", required=True)
    parser.add_argument("--caller-uid", type=int, required=True)
    args = parser.parse_args()
    if os.geteuid() != 0 or args.caller_uid <= 0:
        parser.error("run trusted manager as root on dedicated execution node; caller must be nonroot")
    address = Path(args.socket)
    if not address.parent.is_dir() or address.exists() or address.is_symlink():
        parser.error("socket parent must exist and socket path must be unused; never blindly unlink")
    manager = Manager(args.state, args.policy)
    server = Server(str(address), manager, args.caller_uid)
    os.chown(address, 0, 0)
    os.chmod(address, 0o666)  # SO_PEERCRED authorizes exact UID before reading any bytes.
    def shutdown(_signum, _frame):
        threading.Thread(target=server.shutdown, daemon=True).start()
    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)
    try:
        server.serve_forever(poll_interval=0.2)
    finally:
        server.server_close()
        address.unlink()
        manager.close()


if __name__ == "__main__":
    main()
