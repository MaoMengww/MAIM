#!/usr/bin/env python3
"""Privileged, real-Docker acceptance; never a production execution endpoint.

Run only after independently reviewing and freezing the worker and policy:
  python3 execution/docker/accept.py --policy /path/policy.json \
      --state /private/accept-state --evidence /private/new-evidence

No image is built, pulled, approved, or selected by this program. Evidence is
bounded, private, and distinguishes observations from missing confirmation.
"""
from __future__ import annotations

import argparse
import base64
import copy
import errno
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import select
import signal
import socket
import stat
import tempfile
import threading
import time
import uuid
import subprocess
import sys


CSV_INPUT = b"value\n10\n20\n30\n"
CSV_OUTPUT = b"rows,total,mean,min,max\n3,60,20,10,30\n"
ENTRYPOINT = ["python3", "-I", "-B", "/opt/aim/worker.py"]
MAX_RAW = 2 * 1024 * 1024
MAX_EVIDENCE = 32 * 1024 * 1024
CASES = (
    "csv", "security-network-isolation", "cpu-memory-swap-cgroups",
    "tmpfs-hard-limits", "memory-oom", "pids-hard-limit", "script-failure",
    "total-timeout", "step-timeout", "explicit-stop-descendants",
    "output-byte-limit", "output-file-limit", "log-hard-limit",
    "input-and-limit-rejection", "concurrency-and-replay", "client-close-recovery",
    "daemon-unavailable-recovery", "unsupported-daemon-rejection",
    "foreign-resource-sentinel", "final-cleanup",
)


def encoded(value: bytes) -> str:
    return base64.b64encode(value).decode("ascii")


def utc_now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def error_fact(exc: Exception) -> dict:
    return {"type": type(exc).__name__, "message": str(exc)[:2048]}


class Evidence:
    def __init__(self, directory: Path):
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        if directory.is_symlink() or any(directory.iterdir()):
            raise ValueError("--evidence must be a new or empty non-symlink directory")
        directory.chmod(0o700)
        self.directory = directory
        self.count = 0
        self.bytes = 0
        self.incomplete = []

    def save(self, label: str, value: object) -> str:
        self.count += 1
        name = f"{self.count:04d}-{label}.json"
        data = (json.dumps(value, sort_keys=True, ensure_ascii=True, allow_nan=False,
                           indent=2) + "\n").encode("utf-8")
        if len(data) > MAX_RAW or self.bytes + len(data) > MAX_EVIDENCE - MAX_RAW:
            self.incomplete.append(name)
            data = (json.dumps({
                "unconfirmed": "Raw evidence exceeded the explicit evidence budget",
                "original_bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(),
                "prefix_utf8": data[:4096].decode("utf-8", errors="replace"),
            }, indent=2) + "\n").encode()
        if self.bytes + len(data) > MAX_EVIDENCE - MAX_RAW:
            if name not in self.incomplete:
                self.incomplete.append(name)
            return "unrecorded-evidence-budget-exhausted:" + name
        with (self.directory / name).open("xb") as output:
            os.chmod(output.fileno(), 0o600)
            output.write(data)
        self.bytes += len(data)
        return name

    def summary(self, value: dict) -> None:
        data = (json.dumps(value, indent=2, sort_keys=True, allow_nan=False) + "\n").encode()
        if len(data) > MAX_RAW or self.bytes + len(data) > MAX_EVIDENCE:
            raise RuntimeError("Summary exceeds evidence budget; acceptance is unconfirmed")
        with (self.directory / "summary.json").open("xb") as output:
            os.chmod(output.fileno(), 0o600)
            output.write(data)


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path: str):
        super().__init__("localhost", timeout=8)
        self.path = path

    def connect(self) -> None:
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


class Docker:
    """Only trusted acceptance code has this local daemon authority."""
    def __init__(self, path: str, evidence: Evidence):
        self.path = path
        self.evidence = evidence

    def call(self, method: str, path: str, body: dict | None = None) -> tuple[int, object]:
        connection = UnixHTTP(self.path)
        try:
            payload = None if body is None else json.dumps(body).encode()
            connection.request(method, path, body=payload,
                               headers={"Content-Type": "application/json"})
            response = connection.getresponse()
            raw = response.read(MAX_RAW + 1)
            if len(raw) > MAX_RAW:
                raise RuntimeError("Docker response exceeds the raw evidence bound")
            value = json.loads(raw) if raw else None
            return response.status, value
        finally:
            connection.close()

    def inspect(self, identity: str) -> tuple[int, object]:
        if not re.fullmatch(r"[0-9a-f]{64}", identity or ""):
            raise ValueError("Refusing Docker access without exact full container ID")
        return self.call("GET", f"/containers/{identity}/json")


class SocketGate:
    """Byte-for-byte Unix proxy; outage cuts transport to the REAL daemon.

    No Docker responses are fabricated. Only this suite's separate manager is
    routed through the gate; the actual daemon and other clients remain intact.
    """
    def __init__(self, destination: str):
        self.destination = destination
        self.directory = Path(tempfile.mkdtemp(prefix="aim-accept-gate-"))
        self.path = self.directory / "docker.sock"
        self.listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.listener.bind(str(self.path))
        self.path.chmod(0o600)
        self.inode = self.path.stat().st_ino
        self.listener.listen(16)
        self.listener.settimeout(0.2)
        self.lock = threading.Lock()
        self.clients = set()
        self.enabled = True
        self.closed = False
        self.slots = threading.BoundedSemaphore(16)
        self.thread = threading.Thread(target=self._accept, daemon=True)
        self.thread.start()

    def _accept(self) -> None:
        while not self.closed:
            try:
                client, _ = self.listener.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            if not self.slots.acquire(blocking=False):
                client.close()
                continue
            threading.Thread(target=self._relay, args=(client,), daemon=True).start()

    def _relay(self, client: socket.socket) -> None:
        upstream = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            with self.lock:
                if not self.enabled or self.closed:
                    return
                self.clients.update((client, upstream))
            upstream.settimeout(3)
            upstream.connect(self.destination)
            upstream.settimeout(None)
            while not self.closed:
                with self.lock:
                    if not self.enabled:
                        return
                readable, _, _ = select.select((client, upstream), (), (), 0.2)
                for source in readable:
                    chunk = source.recv(65536)
                    if not chunk:
                        return
                    target = upstream if source is client else client
                    target.settimeout(3)
                    target.sendall(chunk)
                    target.settimeout(None)
        except (OSError, ValueError):
            pass
        finally:
            with self.lock:
                self.clients.discard(client)
                self.clients.discard(upstream)
            client.close()
            upstream.close()
            self.slots.release()

    def pause(self) -> None:
        with self.lock:
            self.enabled = False
            for client in tuple(self.clients):
                try:
                    client.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                client.close()

    def resume(self) -> None:
        with self.lock:
            self.enabled = True

    def close(self) -> None:
        self.closed = True
        self.pause()
        self.listener.close()
        self.thread.join(timeout=2)
        if self.path.exists() and self.path.lstat().st_ino == self.inode:
            self.path.unlink()
        self.directory.rmdir()


class Acceptance:
    def __init__(self, args: argparse.Namespace, evidence: Evidence, module):
        self.args = args
        self.evidence = evidence
        self.module = module
        self.docker = Docker(args.docker_socket, evidence)
        self.manager = None
        self.results = {}
        self.current = None
        self.records = {}
        self.requests = {}
        self.main_execution_ids = set()
        self.interrupted = None
        self.deadline = time.monotonic() + 600
        self.sentinel = None
        self.sentinel_labels = None
        self.marker = evidence.directory / "foreign-resource-sentinel.bin"
        self.marker_bytes = os.urandom(64)
        with self.marker.open("xb") as output:
            os.chmod(output.fileno(), 0o600)
            output.write(self.marker_bytes)
        self.evidence.bytes += len(self.marker_bytes)

    def interrupt(self, signum, _frame) -> None:
        self.interrupted = signum
        self.deadline = 0

    def check(self, condition: bool, statement: str, observed=None,
              available: bool = True) -> None:
        self.results[self.current]["observations"].append({
            "status": ("passed" if condition else "failed") if available else "unconfirmed",
            "statement": statement, "observed": observed,
        })

    def case(self, name: str, function) -> None:
        self.current = name
        self.results[name] = {"observations": []}
        try:
            if name != "final-cleanup" and time.monotonic() >= self.deadline:
                raise TimeoutError("Acceptance suite's 600-second wall budget expired")
            function()
        except Exception as exc:
            self.check(False, "Scenario completed with real observable evidence",
                       error_fact(exc), available=False)
        observations = self.results[name]["observations"]
        statuses = {item["status"] for item in observations}
        self.results[name]["status"] = (
            "failed" if "failed" in statuses else
            "unconfirmed" if "unconfirmed" in statuses or not statuses else "passed")

    def request(self, operation="csv", data=CSV_INPUT, **limits) -> dict:
        budgets = dict(self.module.DEFAULT_LIMITS)
        budgets.update(limits)
        return {"run_id": "accept_" + uuid.uuid4().hex, "generation": 1,
                "capability": "csv-v1" if operation == "csv" else operation,
                "input_b64": encoded(data), "limits": budgets}

    def submit(self, request: dict, manager=None, probe: bool | None = None) -> dict:
        if time.monotonic() >= self.deadline:
            raise TimeoutError("No further admission after interruption or acceptance deadline")
        manager = manager or self.manager
        if probe is None:
            probe = request["capability"] != "csv-v1"
        record = manager.submit(request, probe=probe)
        identity = record["execution_id"]
        self.requests[identity] = copy.deepcopy(request)
        self.records[identity] = record
        if manager is self.manager:
            self.main_execution_ids.add(identity)
        self.evidence.save("submit", {"request": request, "record": record})
        return record

    def settled(self, record: dict, manager=None, seconds: float | None = None) -> dict:
        manager = manager or self.manager
        identity = record["execution_id"]
        budget = seconds if seconds is not None else record["limits"]["total_seconds"] + 25
        deadline = min(self.deadline, time.monotonic() + budget)
        while time.monotonic() < deadline:
            record = manager.get(identity)
            if record.get("phase") in ("cleaned", "unknown"):
                break
            time.sleep(0.1)
        self.records[identity] = record
        self.evidence.save("final-record", record)
        if record.get("phase") not in ("cleaned", "unknown"):
            self.check(False, "Execution converged within its bounded lifecycle", record.get("phase"),
                       available=False)
        return record

    def running(self, record: dict, manager=None) -> dict:
        manager = manager or self.manager
        deadline = min(self.deadline, time.monotonic() + 12)
        while time.monotonic() < deadline:
            record = manager.get(record["execution_id"])
            if record.get("phase") == "running":
                time.sleep(1)
                return manager.get(record["execution_id"])
            if record.get("phase") in ("cleaned", "unknown"):
                break
            time.sleep(0.1)
        return record

    def cleanup_check(self, record: dict, expected: tuple[str, ...] | None = None) -> None:
        evidence = record.get("evidence", {})
        known = record.get("phase") != "unknown"
        self.check(record.get("phase") == "cleaned", "Lifecycle reports confirmed cleanup",
                   {"phase": record.get("phase"), "error": record.get("error")}, available=known)
        if expected:
            self.check(record.get("outcome") in expected, "Observed expected execution outcome",
                       {"expected": expected, "actual": record.get("outcome")}, available=known)
        self.check(evidence.get("removed") is True and evidence.get("resources_absent") is True,
                   "Manager confirmed exact container and local writable resource removal",
                   {key: evidence.get(key) for key in ("removed", "resources_absent", "resource_directory",
                                                      "stop_confirmed", "host_pids", "cgroup")}, available=known)
        self.check(isinstance(evidence.get("wait"), (dict, int)) and
                   not isinstance(evidence.get("wait"), bool),
                   "Docker Wait exit evidence was retained", evidence.get("wait"), available=known)
        identity = record.get("container_id")
        if identity:
            status, result = self.docker.inspect(identity)
            self.evidence.save("post-clean-inspect", {"id": identity, "status": status, "body": result})
            self.check(status == 404, "Independent exact-ID Inspect confirms container absence", status)
        else:
            self.check(False, "Actual container identity was recorded before cleanup", available=False)

    def execute(self, operation="csv", data=CSV_INPUT, expected=("succeeded",), **limits) -> dict:
        request = self.request(operation, data, **limits)
        record = self.settled(self.submit(request))
        self.cleanup_check(record, expected)
        return record

    def facts(self, record: dict) -> dict:
        result = record.get("result")
        facts = result.get("facts") if isinstance(result, dict) else None
        self.check(isinstance(facts, dict), "Fixed worker returned actual structured probe facts",
                   facts, available=isinstance(facts, dict))
        return facts if isinstance(facts, dict) else {}

    def snapshot(self, record: dict) -> dict:
        identity = record.get("container_id")
        if not identity:
            self.check(False, "Running container has exact identity", available=False)
            return {}
        status, inspection = self.docker.inspect(identity)
        self.evidence.save("running-inspect", {"id": identity, "status": status, "body": inspection})
        if status != 200:
            self.check(False, "Running container can be independently inspected", status, available=False)
            return {}
        status, top = self.docker.call("GET", f"/containers/{identity}/top")
        self.evidence.save("running-top", {"id": identity, "status": status, "body": top})
        processes = []
        if status == 200 and isinstance(top, dict):
            titles = top.get("Titles", [])
            index = titles.index("PID") if "PID" in titles else None
            if index is not None:
                for row in top.get("Processes", [])[:64]:
                    try:
                        pid = int(row[index])
                        raw_stat = Path(f"/proc/{pid}/stat").read_text()[:8192]
                        tail = raw_stat[raw_stat.rfind(")") + 2:].split()
                        processes.append({"pid": pid, "starttime": tail[19],
                                          "cgroup": Path(f"/proc/{pid}/cgroup").read_text()[:8192]})
                    except (ValueError, IndexError, OSError) as exc:
                        processes.append({"unconfirmed": error_fact(exc), "row": row})
        self.evidence.save("host-processes-before-stop", processes)
        return {"inspect": inspection, "processes": processes, "top": top}

    def process_absent(self, snapshot: dict) -> None:
        processes = snapshot.get("processes", [])
        self.check(len(processes) >= 3, "Actual host PID snapshot includes parent and descendants",
                   processes, available=bool(processes))
        for process in processes:
            if "pid" not in process:
                self.check(False, "Host process identity was readable", process, available=False)
                continue
            try:
                raw = Path(f"/proc/{process['pid']}/stat").read_text()[:8192]
                tail = raw[raw.rfind(")") + 2:].split()
                gone = tail[19] != process["starttime"]
                self.check(gone, "Original host process no longer exists (PID reuse checked)", process)
            except FileNotFoundError:
                self.check(True, "Original host process is absent", process)
            except (OSError, IndexError) as exc:
                self.check(False, "Original host process absence is confirmed", error_fact(exc), available=False)

    def prepare(self) -> None:
        policy_path = Path(self.args.policy)
        with policy_path.open("rb") as source:
            raw = source.read(100001)
        if len(raw) > 100000:
            raise ValueError("Policy exceeds acceptance read bound")
        self.policy = json.loads(raw)
        metadata = policy_path.stat()
        self.evidence.save("operator-supplied-policy", {
            "policy": self.policy, "sha256": hashlib.sha256(raw).hexdigest(),
            "uid": metadata.st_uid, "mode": oct(stat.S_IMODE(metadata.st_mode)),
            "root_owned_not_writable": metadata.st_uid == 0 and metadata.st_mode & 0o222 == 0,
            "approval": "Operator supplied; this CLI neither performs nor attests human review",
        })
        status, version = self.docker.call("GET", "/version")
        self.evidence.save("docker-version", {"status": status, "body": version})
        if status != 200:
            raise RuntimeError("Real local Docker daemon is unavailable")
        status, info = self.docker.call("GET", "/info")
        if status != 200 or not isinstance(info, dict):
            raise RuntimeError("Cannot inspect actual Docker host capabilities")
        self.info = info
        fields = ("ID", "ServerVersion", "KernelVersion", "OperatingSystem", "OSType", "Architecture",
                  "Driver", "DriverStatus", "DockerRootDir", "CgroupDriver", "CgroupVersion",
                  "SecurityOptions", "MemoryLimit", "SwapLimit", "PidsLimit", "CpuCfsPeriod",
                  "CpuCfsQuota", "NCPU", "MemTotal", "Warnings")
        self.evidence.save("docker-host-observed-fields", {
            "status": status, "projection": {key: info.get(key) for key in fields},
            "note": "Actual API field projection; proxy settings and other possible secrets omitted",
        })
        self.manager = self.module.Manager(self.args.state, self.args.policy,
                                           docker_socket=self.args.docker_socket)
        self.manager.recover()
        image = self.policy["image_id"]
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", image):
            raise ValueError("Policy image must be a pinned full image ID")
        self.sentinel_labels = {"aim.execution.owner": "foreign-" + uuid.uuid4().hex,
                                "aim.execution.id": uuid.uuid4().hex,
                                "aim.acceptance.sentinel": "true"}
        config = {
            "Image": image, "Entrypoint": ENTRYPOINT, "Cmd": [], "User": "65532:65532",
            "Labels": self.sentinel_labels, "NetworkDisabled": True,
            "HostConfig": {"ReadonlyRootfs": True, "NetworkMode": "none", "IpcMode": "none",
                           "CapDrop": ["ALL"], "SecurityOpt": ["no-new-privileges:true"],
                           "Memory": 32 * 1024 * 1024, "MemorySwap": 32 * 1024 * 1024,
                           "PidsLimit": 16, "NanoCpus": 100000000,
                           "LogConfig": {"Type": "none", "Config": {}}},
        }
        name = "aim-accept-sentinel-" + uuid.uuid4().hex
        self.evidence.save("sentinel-create-intent", {"name": name, "config": config})
        status, response = self.docker.call("POST", "/containers/create?name=" + name, config)
        self.evidence.save("sentinel-create-response", {"status": status, "body": response})
        if status != 201:
            raise RuntimeError("Could not create an unrelated real Docker cleanup sentinel")
        self.sentinel = response["Id"]
        if not re.fullmatch(r"[0-9a-f]{64}", self.sentinel):
            raise RuntimeError("Sentinel create returned no exact full container identity")

    def csv(self) -> None:
        record = self.execute()
        result = record.get("result") or {}
        files = result.get("files", [])
        output = None
        if len(files) == 1 and files[0].get("name") == "result.csv":
            output = base64.b64decode(files[0].get("data_b64", ""), validate=True)
        self.check(output == CSV_OUTPUT, "CSV bytes exactly match the required two LF-terminated rows",
                   {"actual_b64": encoded(output) if output is not None else None,
                    "expected_b64": encoded(CSV_OUTPUT), "input_bytes": len(CSV_INPUT),
                    "output_bytes": len(output) if output is not None else None})
        facts = self.facts(record)
        self.check(record.get("input_sha256") == hashlib.sha256(CSV_INPUT).hexdigest(),
                   "Manager records exact input identity", record.get("input_sha256"))
        self.check(record.get("image_id") == self.policy["image_id"] and
                   record.get("worker_sha256") == self.policy["worker_sha256"],
                   "Actual execution records reviewed immutable image and worker identities",
                   {key: record.get(key) for key in ("image_id", "worker_sha256")})
        self.check(facts.get("input_seals", 0) & 15 == 15,
                   "Actual input memfd has seal/shrink/grow/write seals", facts.get("input_seals"),
                   available="input_seals" in facts)

    def security(self) -> None:
        packets = []
        for data in (b"tenant-A-secret-" + uuid.uuid4().hex.encode(),
                     b"tenant-B-secret-" + uuid.uuid4().hex.encode()):
            record = self.execute("probe-security", data, total_seconds=20, step_seconds=15)
            facts = self.facts(record)
            packets.append((record, facts))
            self.check(facts.get("uid") == 65532 and facts.get("gid") == 65532,
                       "Actual workload UID/GID are non-root fixed identities",
                       {"uid": facts.get("uid"), "gid": facts.get("gid")}, available=bool(facts))
            status = facts.get("status", {})
            self.check(all(int(status.get(key, "-1"), 16) == 0 for key in ("CapEff", "CapPrm", "CapBnd")),
                       "Actual effective/permitted/bounding capabilities are empty", status, available=bool(status))
            self.check(str(status.get("NoNewPrivs")) == "1" and str(status.get("Seccomp")) == "2",
                       "Actual NoNewPrivs and seccomp filtering are active", status, available=bool(status))
            writes = facts.get("writes", [])
            self.check(len(writes) >= 2 and all(item.get("writable") is False and
                       item.get("errno") in (errno.EROFS, errno.EACCES, errno.EPERM) for item in writes),
                       "Actual root and package writes are denied", writes, available=bool(writes))
            self.check(facts.get("workspace_entries_before") == [] and facts.get("tmp_entries_before") == [],
                       "Fresh workspace/tmp expose no earlier run input or marker",
                       {key: facts.get(key) for key in ("workspace_entries_before", "tmp_entries_before")},
                       available="workspace_entries_before" in facts and "tmp_entries_before" in facts)
            markers = facts.get("isolation_markers", [])
            self.check(len(markers) == 2 and all(item.get("created") is True and
                       item.get("sha256") == hashlib.sha256((hashlib.sha256(data).hexdigest() + "\n").encode()).hexdigest()
                       for item in markers),
                       "Each run actually creates only its own fixed isolation markers", markers,
                       available=bool(markers))
            mutations = facts.get("input_mutation", {})
            self.check(mutations.get("write_errno") == errno.EPERM and
                       mutations.get("truncate_errno") == errno.EPERM,
                       "Actual writes and truncation of sealed input are denied", mutations,
                       available=bool(mutations))
            namespaces = facts.get("namespaces", {})
            for namespace in ("pid", "net", "ipc", "mnt", "cgroup"):
                observed = namespaces.get(namespace)
                host_namespace = os.readlink("/proc/self/ns/" + namespace)
                self.check(isinstance(observed, str) and observed != host_namespace,
                           "Actual workload namespace differs from the host namespace",
                           {"namespace": namespace, "host": host_namespace, "workload": observed},
                           available=isinstance(observed, str))
            network = facts.get("network", [])
            labels = {item.get("label") for item in network}
            needed = {"public_ipv4", "private_ipv4", "host_ipv4", "metadata_ipv4", "dns",
                      "public_ipv6", "private_ipv6"}
            self.check(needed <= labels and all(item.get("connected") is False and
                       (item.get("errno") is not None or item.get("error")) for item in network),
                       "Real direct IPv4/IPv6 public/private/host/metadata and DNS attempts all fail",
                       network, available=bool(network))
            self.inspect_security(record)
            self.mac_check(facts)
        self.check(packets[0][1].get("input_sha256") != packets[1][1].get("input_sha256") and
                   packets[0][0].get("container_id") != packets[1][0].get("container_id"),
                   "Tenant A/B execute distinct input identities and distinct actual containers",
                   [{"input": facts.get("input_sha256"), "container": record.get("container_id")}
                    for record, facts in packets], available=all(bool(facts) for _, facts in packets))

    def inspect_security(self, record: dict) -> None:
        raw = record.get("evidence", {}).get("inspect", {})
        if isinstance(raw, list):
            raw = raw[-1] if raw else {}
        if isinstance(raw, dict) and "HostConfig" not in raw:
            raw = raw.get("running", raw.get("created", raw))
        host = raw.get("HostConfig", {}) if isinstance(raw, dict) else {}
        config = raw.get("Config", {}) if isinstance(raw, dict) else {}
        self.check(host.get("ReadonlyRootfs") is True and host.get("Privileged") is False and
                   host.get("NetworkMode") == "none" and host.get("IpcMode") == "none" and
                   host.get("PidMode", "") != "host" and host.get("CgroupnsMode") == "private" and
                   not host.get("Devices") and not host.get("Binds") and not host.get("PortBindings") and
                   not host.get("VolumesFrom"),
                   "Actual Inspect confirms hardened namespaces, rootfs and no host device/bind/port exposure",
                   host, available=bool(host))
        options = host.get("SecurityOpt", [])
        self.check("ALL" in host.get("CapDrop", []) and
                   any(item.startswith("no-new-privileges") for item in options) and
                   not any("unconfined" in item for item in options),
                   "Actual Inspect enables cap-drop/NNP and never unconfined security", options,
                   available=bool(host))
        self.check(config.get("Entrypoint") == ENTRYPOINT and config.get("User") == "65532:65532" and
                   not config.get("Volumes"), "Actual image/entrypoint/user and volume declaration are fixed",
                   config, available=bool(config))
        mounts = raw.get("Mounts", []) if isinstance(raw, dict) else []
        self.check(all(item.get("Type") == "tmpfs" and item.get("Destination") in
                   ("/workspace", "/tmp", "/dev/shm") for item in mounts),
                   "Actual mounts expose no host files, socket, home, business volumes, or anonymous volumes",
                   mounts, available=bool(raw))
        self.check(host.get("LogConfig", {}).get("Type") == "none" and not raw.get("LogPath"),
                   "Actual Docker daemon workload-log storage is disabled (hard zero bytes)",
                   {"LogConfig": host.get("LogConfig"), "LogPath": raw.get("LogPath")}, available=bool(host))

    def mac_check(self, facts: dict) -> None:
        options = self.info.get("SecurityOptions", [])
        supported = any("apparmor" in item or "selinux" in item for item in options)
        mac = facts.get("mac", {})
        value = mac.get("value", "") if isinstance(mac, dict) else ""
        if supported:
            observed = value if isinstance(value, str) else ""
            self.check(bool(observed.strip()) and "unconfined" not in observed.lower(),
                       "Host-supported MAC has an actual confined workload label", mac,
                       available=bool(observed.strip()))
        else:
            self.check(True, "Daemon reports no AppArmor/SELinux support; no MAC support claimed",
                       {"SecurityOptions": options, "probe": mac})

    def resources(self) -> None:
        record = self.execute("probe-resources", cpu_millis=100, step_seconds=4, total_seconds=20)
        facts = self.facts(record)
        self.cgroup_limits(record, facts.get("cgroup", {}))
        cpu = facts.get("cpu", {})
        before = cpu.get("stat_before", {})
        after = cpu.get("stat_after", {})
        if isinstance(before, str):
            before = self.key_values(before)
        if isinstance(after, str):
            after = self.key_values(after)
        delta = int(after.get("nr_throttled", 0)) - int(before.get("nr_throttled", 0))
        self.check(delta > 0 and cpu.get("cpu_seconds", 0) > 0,
                   "Actual bounded CPU load consumes CPU and triggers cgroup throttling", cpu,
                   available=bool(cpu))

    @staticmethod
    def key_values(text: str) -> dict:
        return dict(line.split(None, 1) for line in text.splitlines() if len(line.split(None, 1)) == 2)

    def cgroup_limits(self, record: dict, cgroup: dict) -> None:
        limits = record["limits"]
        cpu = str(cgroup.get("cpu.max", "")).split()
        cpu_ok = len(cpu) == 2 and cpu[0] != "max" and int(cpu[0]) * 1000 == int(cpu[1]) * limits["cpu_millis"]
        self.check(cpu_ok and str(cgroup.get("memory.max", "")).strip() == str(limits["memory_bytes"]) and
                   str(cgroup.get("memory.swap.max", "")).strip() == "0" and
                   str(cgroup.get("pids.max", "")).strip() == str(limits["pids"]),
                   "Actual cgroup CPU/memory/zero-swap/PIDs match finite requested bounds",
                   cgroup, available=bool(cgroup))

    def disk(self) -> None:
        record = self.execute("probe-disk", workspace_bytes=1024 * 1024, tmp_bytes=1024 * 1024,
                              step_seconds=15, total_seconds=20)
        facts = self.facts(record)
        paths = facts.get("paths", [])
        self.check({item.get("path") for item in paths} == {"/workspace", "/tmp", "/dev/shm"} and
                   all(item.get("hard_limit_reached") is True and item.get("errno") == errno.ENOSPC and
                       0 < item.get("written_bytes", 0) <= item.get("capacity_bytes", 0) for item in paths),
                   "Real writes fill every writable tmpfs to ENOSPC rather than retrieval truncation",
                   paths, available=bool(paths))
        for item in paths:
            budget = record["limits"]["workspace_bytes"] if item.get("path") == "/workspace" else (
                record["limits"]["tmp_bytes"] if item.get("path") == "/tmp" else 1024 * 1024)
            self.check(item.get("capacity_bytes") == budget, "Actual tmpfs capacity equals hard declared budget", item)
        before = facts.get("cgroup_before", {})
        initial_shmem = self.key_values(str(before.get("memory.stat", ""))).get("shmem")
        for item in paths:
            charged = item.get("cgroup_at_limit", {})
            shmem = self.key_values(str(charged.get("memory.stat", ""))).get("shmem")
            self.check(shmem is not None and initial_shmem is not None and
                       int(shmem) > int(initial_shmem) and
                       0 < int(charged.get("memory.current", 0)) <= record["limits"]["memory_bytes"],
                       "Actual full tmpfs is charged as shared memory under the hard memory budget",
                       {"path": item.get("path"), "before": before, "at_limit": charged},
                       available=bool(charged) and initial_shmem is not None and shmem is not None)

    def memory(self) -> None:
        record = self.execute("probe-memory", expected=("failed",), memory_bytes=64 * 1024 * 1024,
                              step_seconds=10, total_seconds=15)
        evidence = record.get("evidence", {})
        candidates = [evidence.get("stop_snapshot"), evidence.get("inspect")]
        oom = False
        for item in candidates:
            if isinstance(item, dict):
                for candidate in (item, item.get("running", {}), item.get("stopped", {})):
                    if isinstance(candidate, dict) and candidate.get("State", {}).get("OOMKilled") is True:
                        oom = True
        self.check(oom, "Real memory allocation was killed by the container memory controller",
                   {"inspect": evidence.get("inspect"), "stop_snapshot": evidence.get("stop_snapshot")},
                   available=bool(evidence.get("stop_snapshot") or evidence.get("inspect")))

    def pids(self) -> None:
        record = self.execute("probe-pids", pids=16, step_seconds=10, total_seconds=15)
        facts = self.facts(record)
        before, after = facts.get("cgroup_before", {}), facts.get("cgroup_after", {})
        events_before = self.key_values(str(before.get("pids.events", "")))
        events_after = self.key_values(str(after.get("pids.events", "")))
        self.check(facts.get("count", 0) > 0 and facts.get("errno") == errno.EAGAIN and
                   int(events_after.get("max", 0)) > int(events_before.get("max", 0)) and
                   facts.get("children_reaped") == facts.get("count"),
                   "Actual bounded fork load hits PIDs controller and reaps children", facts, available=bool(facts))

    def stop(self) -> None:
        record = self.running(self.submit(self.request("probe-process", total_seconds=30, step_seconds=30)))
        snapshot = self.snapshot(record)
        stopped = self.manager.stop(record["execution_id"])
        self.evidence.save("explicit-stop-response", stopped)
        record = self.settled(record)
        self.cleanup_check(record, ("stopped",))
        facts = self.facts(record)
        self.check(facts.get("ready") is True and bool(facts.get("children")) and bool(facts.get("grandchildren")),
                   "Fixed process really created background child and grandchild before stop", facts,
                   available=bool(facts))
        self.process_absent(snapshot)

    def timeout(self, total_seconds: int, step_seconds: int) -> None:
        record = self.running(self.submit(self.request("probe-process", total_seconds=total_seconds,
                                                       step_seconds=step_seconds)))
        snapshot = self.snapshot(record)
        record = self.settled(record)
        self.cleanup_check(record, ("timeout",))
        self.process_absent(snapshot)

    def log_limit(self) -> None:
        record = self.execute("probe-log", expected=("output_limit",), log_bytes=4096)
        facts = record.get("evidence", {})
        count = facts.get("attached_bytes")
        self.check(isinstance(count, int) and 0 <= count <= 4096,
                   "Actual continuous log probe is stopped with bounded attached stdout/stderr",
                   {"attached_bytes": count, "limit": 4096}, available=isinstance(count, int))
        self.inspect_security(record)


    def rejection(self, request: dict, probe=False) -> None:
        self.evidence.save("rejection-request", {"python_request_repr": repr(request),
                                               "transport": "trusted in-process Manager.submit"})
        try:
            record = self.submit(request, probe=probe)
        except Exception as exc:
            self.evidence.save("rejection-response", error_fact(exc))
            self.check(isinstance(exc, self.module.Rejected), "Invalid request is rejected before admission",
                       error_fact(exc))
        else:
            self.check(False, "Invalid request is rejected before admission", record)
            try:
                self.manager.stop(record["execution_id"])
                self.settled(record)
            except Exception as exc:
                self.check(False, "Unexpectedly admitted invalid request is cleaned", error_fact(exc), available=False)

    def invalid(self) -> None:
        for name in self.module.DEFAULT_LIMITS:
            for mutation in ("missing", "zero", "negative", "boolean", "nonfinite", "nan", "too-large"):
                request = self.request()
                if mutation == "missing":
                    del request["limits"][name]
                else:
                    request["limits"][name] = {"zero": 0, "negative": -1, "boolean": True,
                                                "nonfinite": float("inf"), "nan": float("nan"),
                                                "too-large": 10 ** 30}[mutation]
                self.rejection(request)
        request = self.request()
        del request["limits"]
        self.rejection(request)
        request = self.request(data=b"x" * 65537)
        self.rejection(request)
        self.rejection(self.request(input_bytes=8))
        request = self.request()
        request["input_b64"] = "!!!not-base64!!!"
        self.rejection(request)
        for key, value in (("image", "python:latest"), ("entrypoint", ["sh"]),
                           ("mounts", ["/:/host"]), ("network", "host")):
            request = self.request()
            request[key] = value
            self.rejection(request)
        self.rejection(self.request("probe-security"), probe=False)
        self.rejection(self.request("probe-not-reviewed"), probe=True)
        for data in (b"value\nNaN\n", b"value\nInfinity\n", b"value\n__import__('os')\n"):
            self.execute("csv", data=data, expected=("failed",))
        status, containers = self.docker.call("GET", "/containers/json?all=1")
        self.evidence.save("containers-after-rejections", {"status": status, "body": containers})
        active = [item.get("Id") for item in containers if isinstance(item, dict) and
                  item.get("Labels", {}).get("aim.execution.id") in self.records and
                  item.get("State") == "running"] if isinstance(containers, list) else None
        self.check(active == [], "Invalid requests leave no running owned workload", active,
                   available=status == 200 and isinstance(containers, list))

    def concurrency(self) -> None:
        active = []
        try:
            for _ in range(2):
                active.append(self.submit(self.request("probe-process", total_seconds=30, step_seconds=30)))
            active = [self.running(record) for record in active]
            self.check(all(record.get("phase") == "running" for record in active),
                       "Two actual long-running slots are occupied", [r.get("phase") for r in active])
            self.rejection(self.request("probe-process"), probe=True)
            self.rejection(copy.deepcopy(self.requests[active[0]["execution_id"]]), probe=True)
            conflict = copy.deepcopy(self.requests[active[0]["execution_id"]])
            conflict["input_b64"] = encoded(b"conflicting-input")
            self.rejection(conflict, probe=True)
        finally:
            for record in active:
                self.manager.stop(record["execution_id"])
                self.cleanup_check(self.settled(record), ("stopped",))
        if active:
            self.rejection(copy.deepcopy(self.requests[active[0]["execution_id"]]), probe=True)

    def close_recovery(self) -> None:
        record = self.running(self.submit(self.request("probe-process", total_seconds=20, step_seconds=20)))
        snapshot = self.snapshot(record)
        self.manager.close()
        self.evidence.save("client-close", {"at": utc_now(), "execution_id": record["execution_id"],
                                           "note": "close return alone is NOT stop or cleanup evidence"})
        self.manager = self.module.Manager(self.args.state, self.args.policy,
                                           docker_socket=self.args.docker_socket)
        self.manager.recover()
        recovered = self.manager.get(record["execution_id"])
        self.evidence.save("after-manager-reopen", recovered)
        if recovered.get("phase") not in ("cleaned", "unknown"):
            self.manager.stop(record["execution_id"])
        final = self.settled(record, seconds=30)
        self.cleanup_check(final, ("stopped", "timeout"))
        self.process_absent(snapshot)

    def outage(self) -> None:
        gate = SocketGate(self.args.docker_socket)
        manager = None
        record = None
        try:
            state = Path(self.args.state) / ("outage-" + uuid.uuid4().hex)
            manager = self.module.Manager(str(state), self.args.policy, docker_socket=str(gate.path))
            record = self.running(self.submit(self.request("probe-process", total_seconds=30, step_seconds=30),
                                              manager=manager), manager=manager)
            snapshot = self.snapshot(record)
            gate.pause()
            response = manager.stop(record["execution_id"])
            self.evidence.save("real-daemon-transport-interrupted", response)
            deadline = time.monotonic() + 12
            observed = response
            while time.monotonic() < deadline:
                observed = manager.get(record["execution_id"])
                if observed.get("phase") == "unknown":
                    break
                time.sleep(0.2)
            self.evidence.save("daemon-unreachable-record", observed)
            self.check(observed.get("phase") == "unknown" and observed.get("outcome") != "succeeded" and
                       observed.get("evidence", {}).get("resources_absent") is not True,
                       "Actual daemon transport loss preserves unknown; never claims successful cleanup",
                       observed)
            status, actual = self.docker.inspect(record["container_id"])
            self.evidence.save("direct-daemon-during-outage", {"status": status, "body": actual})
            self.check(status == 200, "Real container still exists while manager cannot confirm cleanup", status)
            gate.resume()
            manager.recover()
            deadline = min(self.deadline, time.monotonic() + 35)
            while time.monotonic() < deadline:
                final = manager.get(record["execution_id"])
                if final.get("phase") == "cleaned":
                    break
                time.sleep(0.2)
            else:
                final = manager.get(record["execution_id"])
            self.records[record["execution_id"]] = final
            self.evidence.save("daemon-recovery-final", final)
            self.cleanup_check(final, ("stopped", "timeout"))
            self.process_absent(snapshot)
        finally:
            gate.resume()
            if manager is not None:
                if record is not None:
                    try:
                        latest = manager.get(record["execution_id"])
                        if latest.get("phase") != "cleaned":
                            manager.stop(record["execution_id"])
                            self.settled(record, manager=manager, seconds=20)
                    except Exception as exc:
                        self.check(False, "Outage run remains under confirmed recovery responsibility",
                                   error_fact(exc), available=False)
                try:
                    manager.close()
                    if record is not None:
                        final = manager.get(record["execution_id"])
                        self.records[record["execution_id"]] = final
                        self.evidence.save("outage-manager-final-close", final)
                finally:
                    gate.close()
            else:
                gate.close()

    def unsupported(self) -> None:
        state = Path(self.args.state) / ("negative-daemon-" + uuid.uuid4().hex)
        unavailable = "/run/aim-absent-" + uuid.uuid4().hex + ".sock"
        if os.path.lexists(unavailable):
            raise RuntimeError("Random unavailable-daemon path unexpectedly exists")
        manager = None
        try:
            manager = self.module.Manager(str(state), self.args.policy, docker_socket=unavailable)
        except Exception as exc:
            self.check(True, "Real unavailable daemon refuses manager startup with no host fallback", error_fact(exc))
        else:
            self.check(False, "Unsupported daemon is refused before work can be accepted")
        finally:
            if manager is not None:
                manager.close()
        # Only the child's private mount namespace loses cgroup observability.
        # The real daemon stays available; no host controller/module is changed.
        before = Path("/sys/fs/cgroup/cgroup.controllers").read_text()
        child_state = Path(self.args.state) / ("negative-controller-" + uuid.uuid4().hex)
        script = r'''
import json, pathlib, subprocess, sys
sys.path.insert(0, sys.argv[1])
from manager import Manager, Rejected
mounted = subprocess.run(["/usr/bin/mount", "-t", "tmpfs", "-o", "ro,size=4096,nr_inodes=8", "tmpfs", "/sys/fs/cgroup"], capture_output=True)
if mounted.returncode:
    print(json.dumps({"unconfirmed": "private cgroup masking unavailable", "stderr": mounted.stderr.decode(errors="replace")}))
    raise SystemExit(2)
try:
    instance = Manager(sys.argv[2], sys.argv[3])
except FileNotFoundError as exc:
    refused = exc.filename == "/sys/fs/cgroup/cgroup.controllers"
    print(json.dumps({"refused": refused, "missing_path": exc.filename, "mount_namespace": pathlib.Path("/proc/self/mountinfo").read_text()}))
    raise SystemExit(0 if refused else 1)
except Rejected as exc:
    print(json.dumps({"refused": False, "other_rejection": str(exc)}))
    raise SystemExit(1)
else:
    instance.close()
    print(json.dumps({"refused": False, "error": "manager accepted without cgroup observation"}))
    raise SystemExit(1)
'''
        try:
            result = subprocess.run(["/usr/bin/unshare", "--mount", "--propagation", "private", "--fork",
                                     sys.executable, "-B", "-c", script, str(Path(__file__).parent.resolve()),
                                     str(child_state), str(Path(self.args.policy).resolve())],
                                    capture_output=True, timeout=20)
            raw = {"exit_code": result.returncode, "stdout": result.stdout.decode(errors="replace")[:65536],
                   "stderr": result.stderr.decode(errors="replace")[:4096],
                   "host_controllers_unchanged": Path("/sys/fs/cgroup/cgroup.controllers").read_text() == before}
            self.evidence.save("missing-controller-private-namespace", raw)
            available = result.returncode in (0, 1) and bool(result.stdout)
            self.check(result.returncode == 0 and raw["host_controllers_unchanged"],
                       "Available daemon but missing required cgroup observation refuses admission in private namespace",
                       raw, available=available)
        except (OSError, subprocess.TimeoutExpired) as exc:
            self.check(False, "Controlled missing-controller scenario could not run", error_fact(exc), available=False)

    def sentinel_check(self) -> None:
        self.manager.recover()
        self.check(self.marker.read_bytes() == self.marker_bytes,
                   "Unrelated host file is byte-for-byte untouched", {"sha256": hashlib.sha256(self.marker_bytes).hexdigest()})
        if not self.sentinel:
            self.check(False, "Unrelated real Docker resource sentinel exists", available=False)
            return
        status, value = self.docker.inspect(self.sentinel)
        self.evidence.save("foreign-sentinel-after-recovery", {"id": self.sentinel, "status": status, "body": value})
        self.check(status == 200 and value.get("Id") == self.sentinel and
                   value.get("Config", {}).get("Labels") == self.sentinel_labels and
                   value.get("State", {}).get("Running") is False,
                   "Cleanup/recovery did not delete, relabel or start the foreign-owner container", value)

    def remove_sentinel(self) -> None:
        if not self.sentinel:
            return
        status, value = self.docker.inspect(self.sentinel)
        if status != 200 or value.get("Id") != self.sentinel or value.get("Config", {}).get("Labels") != self.sentinel_labels:
            self.check(False, "Sentinel exact-ID and ownership verified before deletion",
                       {"status": status, "body": value}, available=False)
            return
        if value.get("State", {}).get("Running"):
            self.check(False, "Never-started sentinel has no running process; refusing deletion", value)
            return
        status, response = self.docker.call("DELETE", f"/containers/{self.sentinel}?force=false&v=false")
        self.evidence.save("sentinel-delete", {"id": self.sentinel, "status": status, "body": response})
        after, body = self.docker.inspect(self.sentinel)
        self.evidence.save("sentinel-absence", {"id": self.sentinel, "status": after, "body": body})
        self.check(status == 204 and after == 404, "Only acceptance-owned foreign sentinel is precisely removed",
                   {"delete_status": status, "inspect_status": after})

    def finish(self) -> None:
        if self.manager is not None:
            for identity, record in tuple(self.records.items()):
                if identity in self.main_execution_ids and record.get("phase") != "cleaned":
                    try:
                        latest = self.manager.get(identity)
                        if latest.get("phase") != "cleaned":
                            self.manager.stop(identity)
                        self.records[identity] = self.settled(latest, seconds=20)
                    except Exception as exc:
                        self.check(False, "Outstanding execution remains explicitly unconfirmed",
                                   {"execution_id": identity, "error": error_fact(exc)}, available=False)
            try:
                self.manager.close()
                for identity in self.main_execution_ids:
                    self.records[identity] = self.manager.get(identity)
                    self.evidence.save("final-manager-record", self.records[identity])
            except Exception as exc:
                self.check(False, "Manager close/recovery completed with durable responsibility",
                           error_fact(exc), available=False)
            finally:
                self.manager = None
        self.remove_sentinel()
        for identity, record in self.records.items():
            self.check(record.get("phase") == "cleaned" and
                       record.get("evidence", {}).get("resources_absent") is True,
                       "Every admitted execution has durable confirmed cleanup",
                       {"execution_id": identity, "phase": record.get("phase"), "error": record.get("error")},
                       available=record.get("phase") != "unknown")

    def run(self) -> dict:
        setup_error = None
        try:
            self.prepare()
        except Exception as exc:
            setup_error = error_fact(exc)
            self.evidence.save("startup-unconfirmed", setup_error)
        if setup_error is None:
            self.case("csv", self.csv)
            self.case("security-network-isolation", self.security)
            self.case("cpu-memory-swap-cgroups", self.resources)
            self.case("tmpfs-hard-limits", self.disk)
            self.case("memory-oom", self.memory)
            self.case("pids-hard-limit", self.pids)
            self.case("script-failure", lambda: self.execute("probe-failure", expected=("failed",)))
            self.case("total-timeout", lambda: self.timeout(total_seconds=3, step_seconds=3))
            self.case("step-timeout", lambda: self.timeout(total_seconds=15, step_seconds=2))
            self.case("explicit-stop-descendants", self.stop)
            self.case("output-byte-limit", lambda: self.execute("probe-output", expected=("output_limit",), output_bytes=32))
            self.case("output-file-limit", lambda: self.execute("probe-files", expected=("output_limit",), output_files=1))
            self.case("log-hard-limit", self.log_limit)
            self.case("input-and-limit-rejection", self.invalid)
            self.case("concurrency-and-replay", self.concurrency)
            self.case("client-close-recovery", self.close_recovery)
            self.case("daemon-unavailable-recovery", self.outage)
            self.case("unsupported-daemon-rejection", self.unsupported)
            self.case("foreign-resource-sentinel", self.sentinel_check)
        self.case("final-cleanup", self.finish)
        for name in CASES:
            if name not in self.results:
                self.results[name] = {"status": "unconfirmed", "observations": [{
                    "status": "unconfirmed", "statement": "Scenario not exercised because startup was refused",
                    "observed": setup_error}]}
        incomplete = [{"scenario": name, **observation} for name, case in self.results.items()
                      for observation in case["observations"] if observation["status"] != "passed"]
        for name in self.evidence.incomplete:
            incomplete.append({"scenario": "evidence", "status": "unconfirmed",
                               "statement": "Raw evidence exceeded configured bound", "observed": name})
        failed = any(case["status"] == "failed" for case in self.results.values())
        return {
            "schema": "aim.docker.acceptance.v1", "finished_at": utc_now(),
            "status": "failed" if failed else "unconfirmed" if incomplete else "passed",
            "real_docker_only": True, "startup_error": setup_error,
            "scope": "Fixed reviewed worker; not arbitrary-code or MicroVM isolation",
            "human_review": "Not established by this program; operator-provided policy is an independent prerequisite",
            "production_policy_file": "Actual owner/mode recorded; a policy file never replaces human review",
            "context_cancellation": "No task context exists in the Manager contract. Explicit stop, client close, "
                                    "transport failure and independent recovery are exercised instead; no claim of a "
                                    "separate context-cancellation API test.",
            "evidence_bounds": {"per_raw_file_bytes": MAX_RAW, "total_bytes": MAX_EVIDENCE,
                                "scenario_admission_wall_seconds": 600,
                                "cleanup": "Manager's bounded stop/recovery continues after admission deadline"},
            "scenarios": self.results, "unconfirmed_or_failed": incomplete,
            "execution_ids": list(self.records),
            "interrupted_by_signal": self.interrupted,
        }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--policy", required=True, help="Existing operator-reviewed immutable image/worker policy")
    parser.add_argument("--state", required=True, help="Private persistent acceptance Manager state directory")
    parser.add_argument("--evidence", required=True, help="New or empty private raw evidence directory")
    parser.add_argument("--docker-socket", default="/var/run/docker.sock", help="Local Unix Docker socket (no TCP)")
    args = parser.parse_args()
    os.umask(0o077)
    evidence = None
    try:
        state = Path(args.state).resolve()
        destination = Path(args.evidence).resolve()
        policy = Path(args.policy).resolve()
        if state == destination or state in destination.parents or destination in state.parents:
            raise ValueError("--state and --evidence must be separate, non-nested directories")
        if state == policy or state in policy.parents or destination == policy or destination in policy.parents:
            raise ValueError("Policy must reside outside state and evidence directories")
        evidence = Evidence(Path(args.evidence))
        args.state, args.policy = str(state), str(policy)
        # Import only after CLI parsing; --help neither touches Docker nor requires manager readiness.
        import manager as manager_module
        runner = Acceptance(args, evidence, manager_module)
        previous = {signum: signal.signal(signum, runner.interrupt)
                    for signum in (signal.SIGINT, signal.SIGTERM)}
        try:
            summary = runner.run()
        finally:
            for signum, handler in previous.items():
                signal.signal(signum, handler)
        evidence.summary(summary)
        print(json.dumps({"status": summary["status"], "summary": str(destination / "summary.json"),
                          "unconfirmed_or_failed": len(summary["unconfirmed_or_failed"])}, sort_keys=True))
        return 1 if summary["status"] == "failed" else 2 if summary["status"] == "unconfirmed" else 0
    except Exception as exc:
        failure = {"schema": "aim.docker.acceptance.v1", "status": "unconfirmed",
                   "finished_at": utc_now(), "startup_error": error_fact(exc),
                   "unconfirmed_or_failed": ["Acceptance did not complete; no runtime claim is established"]}
        if evidence is not None and not (evidence.directory / "summary.json").exists():
            evidence.summary(failure)
        print(json.dumps(failure, sort_keys=True))
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
