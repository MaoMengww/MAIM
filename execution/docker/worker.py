"""Reviewed fixed CSV capability and finite, local-acceptance-only probes.

No operation accepts code, commands, paths, network targets, or dependencies.
The execution manager is responsible for enforcing the whole-container deadline
and for admitting probe operations only through its trusted local interface.
"""

import base64
import binascii
import csv
import errno
import fcntl
import hashlib
import io
import json
import math
import os
import re
import signal
import socket
import stat
import struct
import sys
import time


MAX_REQUEST_BYTES = 100000
MAX_INPUT_BYTES = 65536
MAX_RESULT_BYTES = 65536
MAX_PROBE_DISK_BYTES = 128 * 1024 * 1024
MAX_PROBE_MEMORY_BYTES = 1024 * 1024 * 1024
PROCESS_SECONDS = 120
OPERATIONS = frozenset((
    "csv", "probe-security", "probe-resources", "probe-disk", "probe-log",
    "probe-process", "probe-memory", "probe-pids", "probe-failure",
    "probe-output", "probe-files",
))
NUMBER = re.compile(r"[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?\Z")
CGROUP_FILES = (
    "cpu.max", "cpu.stat", "memory.max", "memory.current", "memory.peak",
    "memory.stat",
    "memory.swap.max", "memory.swap.current", "memory.events", "pids.max",
    "pids.current", "pids.events",
)
SEALS = fcntl.F_SEAL_SHRINK | fcntl.F_SEAL_GROW | fcntl.F_SEAL_WRITE | fcntl.F_SEAL_SEAL


class Rejected(ValueError):
    pass


def write_all(fd, data):
    remaining = memoryview(data)
    while remaining:
        written = os.write(fd, remaining)
        if written <= 0:
            raise OSError("short write")
        remaining = remaining[written:]


def error_fact(exc):
    return {"errno": exc.errno, "error": exc.strerror or type(exc).__name__}


def read_text(path, maximum=16384):
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as source:
            value = source.read(maximum + 1)
        if len(value) > maximum:
            return {"error": "observation exceeds bound", "truncated": True}
        return value.strip()
    except OSError as exc:
        return error_fact(exc)


def cgroup_facts():
    return {name: read_text("/sys/fs/cgroup/" + name, 4096) for name in CGROUP_FILES}


def object_without_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise Rejected("duplicate JSON key")
        result[key] = value
    return result


def reject_constant(value):
    raise Rejected("nonfinite JSON number")


def read_request():
    raw = sys.stdin.buffer.read(MAX_REQUEST_BYTES + 1)
    if not raw or len(raw) > MAX_REQUEST_BYTES:
        raise Rejected("request byte limit")
    request = json.loads(raw.decode("utf-8"), object_pairs_hook=object_without_duplicates,
                         parse_constant=reject_constant)
    required = {"operation", "input_b64", "step_seconds", "output_bytes", "output_files"}
    if type(request) is not dict or set(request) != required:
        raise Rejected("request fields")
    if type(request["operation"]) is not str or request["operation"] not in OPERATIONS:
        raise Rejected("unknown operation")
    for key, maximum in (("step_seconds", 30), ("output_bytes", 65536), ("output_files", 4)):
        if type(request[key]) is not int or not 1 <= request[key] <= maximum:
            raise Rejected("invalid " + key)
    encoded = request["input_b64"]
    if type(encoded) is not str or len(encoded) > 4 * ((MAX_INPUT_BYTES + 2) // 3):
        raise Rejected("input byte limit")
    try:
        data = base64.b64decode(encoded, validate=True)
    except (ValueError, binascii.Error) as exc:
        raise Rejected("invalid input base64") from exc
    if len(data) > MAX_INPUT_BYTES or base64.b64encode(data).decode("ascii") != encoded:
        raise Rejected("input byte limit or noncanonical base64")
    return request, data


def seal_input(data):
    fd = os.memfd_create("aim-input", os.MFD_CLOEXEC | os.MFD_ALLOW_SEALING)
    try:
        write_all(fd, data)
        fcntl.fcntl(fd, fcntl.F_ADD_SEALS, SEALS)
        actual = fcntl.fcntl(fd, fcntl.F_GET_SEALS)
        if actual & SEALS != SEALS:
            raise Rejected("input sealing unavailable")
        os.lseek(fd, 0, os.SEEK_SET)
        return fd
    except BaseException:
        os.close(fd)
        raise


def input_facts(fd, data):
    return {
        "input_sha256": hashlib.sha256(data).hexdigest(),
        "input_bytes": len(data),
        "input_seals": fcntl.fcntl(fd, fcntl.F_GET_SEALS),
    }


def emit(files, facts, enforce=True):
    payload = json.dumps({"files": files, "facts": facts}, allow_nan=False,
                         ensure_ascii=True, separators=(",", ":")).encode("utf-8") + b"\n"
    if enforce and len(payload) > MAX_RESULT_BYTES:
        raise Rejected("result envelope byte limit")
    write_all(sys.stdout.fileno(), payload)


def safe_create(directory, name):
    directory_fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
                     0o600, dir_fd=directory_fd)
        metadata = os.fstat(fd)
        if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1:
            os.close(fd)
            raise Rejected("output must be a single-link regular file")
        return fd
    finally:
        os.close(directory_fd)


def csv_result(request, fd, data):
    csv.field_size_limit(MAX_INPUT_BYTES)
    with io.TextIOWrapper(os.fdopen(os.dup(fd), "rb"), encoding="utf-8", newline="") as source:
        reader = csv.reader(source, strict=True)
        if next(reader, None) != ["value"]:
            raise Rejected("CSV header must be value")
        values = []
        for row in reader:
            if len(row) != 1:
                raise Rejected("CSV must have one value per row")
            text = row[0].strip()
            if not NUMBER.fullmatch(text):
                raise Rejected("invalid CSV number")
            value = float(text)
            if not math.isfinite(value):
                raise Rejected("nonfinite CSV number")
            values.append(value)
    if not values:
        raise Rejected("CSV has no data rows")
    total = math.fsum(values)
    mean = total / len(values)
    if not math.isfinite(total) or not math.isfinite(mean):
        raise Rejected("nonfinite CSV statistics")
    result = ("rows,total,mean,min,max\n" + str(len(values)) + "," +
              ",".join(format(value, ".17g") for value in (total, mean, min(values), max(values))) +
              "\n").encode("ascii")
    if request["output_files"] < 1 or len(result) > request["output_bytes"]:
        raise Rejected("output budget exceeded")
    output_fd = safe_create("/workspace", "result.csv")
    try:
        write_all(output_fd, result)
        metadata = os.fstat(output_fd)
        if (not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1 or
                metadata.st_size != len(result)):
            raise Rejected("output identity changed")
    finally:
        os.close(output_fd)
    facts = input_facts(fd, data)
    facts["output_bytes"] = len(result)
    facts["output_files"] = 1
    emit([{"name": "result.csv", "data_b64": base64.b64encode(result).decode("ascii")}], facts)


def write_probe(directory):
    path = directory + "/.aim-write-probe"
    try:
        fd = safe_create(directory, ".aim-write-probe")
        try:
            write_all(fd, b"fixed read-only boundary probe\n")
        finally:
            os.close(fd)
        os.unlink(path)
        return {"path": path, "writable": True}
    except OSError as exc:
        return {"path": path, "writable": False, **error_fact(exc)}


def connect_probe(label, address, family, port):
    result = {"label": label, "address": address, "port": port,
              "family": "ipv6" if family == socket.AF_INET6 else "ipv4"}
    try:
        with socket.socket(family, socket.SOCK_STREAM) as connection:
            connection.settimeout(0.15)
            connection.connect((address, port))
        result["connected"] = True
    except OSError as exc:
        result.update(connected=False, **error_fact(exc))
    return result


def dns_probe():
    result = {"label": "dns", "address": "1.1.1.1", "port": 53,
              "family": "ipv4", "query": "example.com", "connected": False}
    # One fixed DNS A query with an independently bounded UDP receive.
    query = b"\xa1\x02\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x07example\x03com\x00\x00\x01\x00\x01"
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as connection:
            connection.settimeout(0.15)
            connection.connect(("1.1.1.1", 53))
            connection.send(query)
            response = connection.recv(512)
            result["response_bytes"] = len(response)
            result["connected"] = len(response) >= 12 and response[:2] == query[:2]
    except OSError as exc:
        result.update(error_fact(exc))
    return result


def directory_entries(path):
    with os.scandir(path) as entries:
        names = []
        for entry in entries:
            if len(names) >= 64:
                raise Rejected("unexpected directory entry count")
            names.append(entry.name)
    return sorted(names)


def security_probe(fd, data):
    facts = input_facts(fd, data)
    status_text = read_text("/proc/self/status")
    if not isinstance(status_text, str):
        raise Rejected("process status unavailable")
    observed = dict(line.split(":", 1) for line in status_text.splitlines() if ":" in line)
    facts.update(uid=os.getuid(), gid=os.getgid(), groups=os.getgroups(),
                 status={name: observed.get(name, "").strip() for name in
                         ("Uid", "Gid", "CapEff", "CapPrm", "CapBnd", "NoNewPrivs", "Seccomp")},
                 mac={"path": "/proc/self/attr/current", "value": read_text("/proc/self/attr/current")},
                 mountinfo=read_text("/proc/self/mountinfo", 24576),
                 cgroup=cgroup_facts(),
                 workspace_entries_before=directory_entries("/workspace"),
                 tmp_entries_before=directory_entries("/tmp"),
                 devices=directory_entries("/dev"),
                 writes=[write_probe("/"), write_probe("/opt/aim")])
    mutations = {}
    for name, mutate in (("write_errno", lambda: os.pwrite(fd, b"x", 0)),
                         ("truncate_errno", lambda: os.ftruncate(fd, len(data) + 1))):
        try:
            mutate()
            mutations[name] = None
        except OSError as exc:
            mutations[name] = exc.errno
    facts["input_mutation"] = mutations
    facts["namespaces"] = {}
    for name in ("pid", "net", "ipc", "mnt", "user", "cgroup"):
        try:
            facts["namespaces"][name] = os.readlink("/proc/self/ns/" + name)
        except OSError as exc:
            facts["namespaces"][name] = error_fact(exc)
    targets = (
        ("public_ipv4", "1.1.1.1", socket.AF_INET, 443),
        ("private_ipv4", "10.0.0.1", socket.AF_INET, 443),
        ("host_ipv4", "172.17.0.1", socket.AF_INET, 2375),
        ("metadata_ipv4", "169.254.169.254", socket.AF_INET, 80),
        ("public_ipv6", "2606:4700:4700::1111", socket.AF_INET6, 443),
        ("private_ipv6", "fd00::1", socket.AF_INET6, 443),
    )
    facts["network"] = [connect_probe(*target) for target in targets] + [dns_probe()]
    facts["isolation_markers"] = []
    marker = (facts["input_sha256"] + "\n").encode("ascii")
    for directory in ("/workspace", "/tmp"):
        marker_fd = safe_create(directory, "isolation-marker")
        try:
            write_all(marker_fd, marker)
        finally:
            os.close(marker_fd)
        facts["isolation_markers"].append({"path": directory + "/isolation-marker",
                                           "created": True,
                                           "sha256": hashlib.sha256(marker).hexdigest()})
    emit([], facts)


def resources_probe(request, fd, data):
    facts = input_facts(fd, data)
    facts["cgroup"] = cgroup_facts()
    stat_before = read_text("/sys/fs/cgroup/cpu.stat", 4096)
    start = time.monotonic()
    cpu_start = time.process_time()
    duration = min(2.0, request["step_seconds"] * 0.7)
    accumulator = 1
    while time.monotonic() - start < duration:
        for _ in range(4096):
            accumulator = (accumulator * 1664525 + 1013904223) & 0xffffffff
    facts["cpu"] = {"wall_seconds": time.monotonic() - start,
                    "cpu_seconds": time.process_time() - cpu_start,
                    "stat_before": stat_before,
                    "stat_after": read_text("/sys/fs/cgroup/cpu.stat", 4096),
                    "accumulator": accumulator}
    emit([], facts)


def fill_path(path, deadline):
    filesystem = os.statvfs(path)
    capacity = filesystem.f_blocks * filesystem.f_frsize
    facts = {"path": path, "capacity_bytes": capacity, "written_bytes": 0,
             "hard_limit_reached": False}
    # Refuse to turn an unexpectedly unbounded filesystem into a host-disk fill.
    if not 0 < capacity <= MAX_PROBE_DISK_BYTES:
        facts["error"] = "filesystem capacity outside fixed probe bound"
        return facts
    output_fd = safe_create(path, ".aim-disk-probe")
    block = b"d" * 65536
    try:
        while facts["written_bytes"] <= capacity + 65536:
            if time.monotonic() >= deadline:
                facts["error"] = "fixed disk probe deadline"
                break
            try:
                written = os.write(output_fd, block)
                facts["written_bytes"] += written
                if written <= 0:
                    facts["error"] = "short disk probe write"
                    break
            except OSError as exc:
                facts.update(error_fact(exc))
                facts["hard_limit_reached"] = exc.errno in (errno.ENOSPC, errno.EDQUOT)
                break
        facts["available_after_bytes"] = os.statvfs(path).f_bavail * filesystem.f_frsize
        facts["cgroup_at_limit"] = cgroup_facts()
    finally:
        os.close(output_fd)
        os.unlink(path + "/.aim-disk-probe")
    return facts


def disk_probe(request, fd, data):
    facts = input_facts(fd, data)
    facts["cgroup_before"] = cgroup_facts()
    deadline = time.monotonic() + request["step_seconds"] * 0.8
    facts["paths"] = [fill_path(path, deadline) for path in ("/workspace", "/tmp", "/dev/shm")]
    facts["cgroup_after"] = cgroup_facts()
    emit([], facts)


def sleep_until(deadline):
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return
        time.sleep(min(0.2, remaining))


def process_probe(fd, data):
    # Descendants stay alive long enough for host-PID observation and explicit
    # stop. Even without a manager the probe terminates after a fixed 120s.
    deadline = time.monotonic() + PROCESS_SECONDS
    read_fd, write_fd = os.pipe2(os.O_CLOEXEC)
    child = os.fork()
    if child == 0:
        os.close(read_fd)
        try:
            grandchild = os.fork()
            if grandchild == 0:
                os.close(write_fd)
                sleep_until(deadline)
                os._exit(0)
            write_all(write_fd, struct.pack("!Q", grandchild))
            os.close(write_fd)
            os.waitpid(grandchild, 0)
            os._exit(0)
        except BaseException:
            os._exit(2)
    os.close(write_fd)
    try:
        grandchild_bytes = os.read(read_fd, 8)
    finally:
        os.close(read_fd)
    if len(grandchild_bytes) != 8:
        raise Rejected("process descendant setup failed")
    facts = input_facts(fd, data)
    facts.update(pid=os.getpid(), children=[child],
                 grandchildren=[struct.unpack("!Q", grandchild_bytes)[0]],
                 ready=True, duration_seconds=PROCESS_SECONDS)
    emit([], facts)
    os.waitpid(child, 0)


def memory_probe(request, fd, data):
    cgroup = cgroup_facts()
    limit = cgroup["memory.max"]
    if not isinstance(limit, str) or not limit.isdecimal():
        raise Rejected("finite cgroup memory limit required")
    limit = int(limit)
    if not 0 < limit <= MAX_PROBE_MEMORY_BYTES - 16 * 1024 * 1024:
        raise Rejected("memory limit outside fixed probe bound")
    # Each byte is touched, not just reserved virtually. With swap disabled by
    # policy, the actual cgroup OOM killer must terminate this process.
    blocks = []
    allocated = 0
    deadline = time.monotonic() + request["step_seconds"] * 0.8
    target = limit + 16 * 1024 * 1024
    try:
        while allocated < target and time.monotonic() < deadline:
            block = bytearray(1024 * 1024)
            for offset in range(0, len(block), 4096):
                block[offset] = 1
            blocks.append(block)
            allocated += len(block)
    except MemoryError:
        facts = input_facts(fd, data)
        facts.update(allocated_bytes=allocated, allocation_failed=True, cgroup=cgroup_facts())
        emit([], facts)
        raise Rejected("allocation failed without observed process OOM kill")
    facts = input_facts(fd, data)
    facts.update(allocated_bytes=allocated, target_bytes=target, oom_observed=False,
                 cgroup_before=cgroup, cgroup_after=cgroup_facts())
    emit([], facts)
    raise Rejected("memory probe survived; OOM boundary unconfirmed")


def pids_probe(request, fd, data):
    facts = input_facts(fd, data)
    facts["cgroup_before"] = cgroup_facts()
    children = []
    deadline = time.monotonic() + request["step_seconds"] * 0.7
    failure = None
    try:
        for _ in range(256):
            if time.monotonic() >= deadline:
                break
            try:
                pid = os.fork()
            except OSError as exc:
                failure = error_fact(exc)
                break
            if pid == 0:
                sleep_until(deadline)
                os._exit(0)
            children.append(pid)
        facts.update(count=len(children), **(failure or {"errno": None, "error": "fixed fork bound reached"}))
        facts["cgroup_after"] = cgroup_facts()
    finally:
        reaped = 0
        for pid in children:
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        for pid in children:
            try:
                os.waitpid(pid, 0)
                reaped += 1
            except ChildProcessError:
                pass
        facts["children_reaped"] = reaped
    emit([], facts)


def log_probe():
    # This is intentionally not a result JSON packet. The manager must bound
    # live attachment bytes and stop the entire container, not just truncate.
    block = b"fixed acceptance log pressure\n" * 128
    for _ in range(4096):
        write_all(sys.stdout.fileno(), block)


def budget_probe(request, fd, data):
    facts = input_facts(fd, data)
    if request["operation"] == "probe-output":
        output = b"x" * (request["output_bytes"] + 1)
        files = [{"name": "result.csv", "data_b64": base64.b64encode(output).decode("ascii")}]
    else:
        files = [{"name": "fixed-" + str(index) + ".txt", "data_b64": "eA=="}
                 for index in range(request["output_files"] + 1)]
    emit(files, facts, enforce=False)


def terminate(signum, frame):
    raise SystemExit(128 + signum)


def step_expired(signum, frame):
    raise Rejected("fixed operation step deadline")


def main():
    os.umask(0o077)
    signal.signal(signal.SIGTERM, terminate)
    signal.signal(signal.SIGINT, terminate)
    signal.signal(signal.SIGALRM, step_expired)
    signal.setitimer(signal.ITIMER_REAL, 30)
    request, data = read_request()
    fd = seal_input(data)
    try:
        operation = request["operation"]
        # Timeout/stop acceptance needs the process tree to remain alive past
        # the manager's step limit; this one fixed probe has its own finite cap.
        signal.setitimer(signal.ITIMER_REAL, 0 if operation == "probe-process" else request["step_seconds"])
        if operation == "csv":
            csv_result(request, fd, data)
        elif operation == "probe-security":
            security_probe(fd, data)
        elif operation == "probe-resources":
            resources_probe(request, fd, data)
        elif operation == "probe-disk":
            disk_probe(request, fd, data)
        elif operation == "probe-process":
            process_probe(fd, data)
        elif operation == "probe-memory":
            memory_probe(request, fd, data)
        elif operation == "probe-pids":
            pids_probe(request, fd, data)
        elif operation == "probe-log":
            log_probe()
        elif operation == "probe-failure":
            raise Rejected("intentional fixed probe failure")
        else:
            budget_probe(request, fd, data)
    finally:
        os.close(fd)
        signal.setitimer(signal.ITIMER_REAL, 0)


if __name__ == "__main__":
    try:
        main()
    except (Rejected, ValueError, OSError, csv.Error, OverflowError) as exc:
        # Never echo caller-supplied CSV cells or request values in diagnostics.
        message = "fixed worker rejected request: " + type(exc).__name__ + "\n"
        write_all(sys.stderr.fileno(), message.encode("ascii"))
        raise SystemExit(2)
