#!/usr/bin/env python3
"""Run the real AIM stack and external acceptance client in an isolated project."""

import argparse
import copy
import json
import math
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import uuid
import time


# name -> the workload behind a compose service. directory/config are relative to
# the repo; role is appended to the service command so one image can host two
# independently scalable roles (bot control/runtime, RAG online/ingest).
APPLICATIONS = {
    "user-service": ("user-service", "user.yaml", "grpc", 50051, []),
    "message-service": ("message-service", "message.yaml", "grpc", 50053, []),
    "file-service": ("file-service", "file.yaml", "grpc", 50054, []),
    "llm-gateway": ("llm-gateway", "llm-gateway.yaml", "grpc", 50056, []),
    "knowledge-base": ("knowledge-base", "knowledge-base.yaml", "grpc", 50057, []),
    "knowledge-ingest": ("knowledge-base", "knowledge-base.yaml", "http", 9118, ["-role", "ingest"]),
    "bot-service": ("bot-service", "bot.yaml", "grpc", 50058, ["-role", "control"]),
    "bot-runtime": ("bot-service", "bot.yaml", "grpc", 50058, ["-role", "runtime"]),
    "realtime-service": ("realtime-service", "realtime.yaml", "http", 8081, []),
    "gateway": ("gateway", "gateway.yaml", "http", 8080, []),
}
OPTIONAL = {"prometheus", "kibana", "grafana"}
CREDENTIALS = {
    "POSTGRES_USER": "aim",
    "POSTGRES_PASSWORD": "aim123",
    "POSTGRES_DB": "aim",
    "MINIO_ROOT_USER": "minioadmin",
    "MINIO_ROOT_PASSWORD": "minioadmin123",
    "NEO4J_PASSWORD": "password123",
    "JWT_SECRET": "aim-dev-secret-key",
    "AIM_ENC_KEY": "Ay+h5wU31Vfy5gITlP1P2cmNtOPkTsnqIupXHqpgutw=",
    "INGEST_EMBEDDING_TOKEN": "e2e-isolated-ingest-budget-token",
}
TAIL_BYTES = 64 * 1024


class LayerFailure(Exception):
    pass


def duration(value):
    match = re.fullmatch(r"([0-9]+(?:\.[0-9]+)?)(ms|s|m)?", value)
    if not match:
        raise argparse.ArgumentTypeError("use a positive duration, e.g. 20s or 1m")
    seconds = float(match[1]) * {None: 1, "ms": .001, "s": 1, "m": 60}[match[2]]
    if not math.isfinite(seconds) or seconds <= 0:
        raise argparse.ArgumentTypeError("duration must be positive and finite")
    return seconds


class Runner:
    def __init__(self, args, directory):
        self.args = args
        self.repo = Path(__file__).resolve().parents[2]
        self.directory = Path(directory)
        self.project = "aim-e2e-" + uuid.uuid4().hex[:12]
        self.env = dict(os.environ)
        # Shell variables take precedence over --env-file. Clear Compose controls
        # and supply only the harness's fixed interpolation credentials.
        for key in list(self.env):
            if key.startswith("COMPOSE_") or key in CREDENTIALS:
                self.env.pop(key)
        self.env.update(CREDENTIALS)
        self.envfile = self.directory / "test.env"
        self.envfile.write_text("".join(f"{key}={value}\n" for key, value in CREDENTIALS.items()))
        self.composefile = self.directory / "compose.json"
        self.prefix = ["docker", "compose", "--env-file", str(self.envfile),
                       "--project-directory", str(self.repo), "--project-name", self.project]
        self.model = None
        self.sequence = 0
        self.compose_written = False
        self.artifacts = None
        if args.artifacts:
            self.artifacts = Path(args.artifacts).resolve() / self.project
            self.artifacts.mkdir(parents=True, exist_ok=False)

    def emit(self, message):
        print(message, flush=True)

    def command(self, layer, argv, timeout=60, capture=False, required=True):
        self.sequence += 1
        output = self.directory / f"{self.sequence:02d}-{layer}.log"
        self.emit(f"[{layer}] {' '.join(argv)}")
        problem = None
        returncode = None
        try:
            with output.open("wb") as stream:
                process = subprocess.Popen(argv, cwd=self.repo, env=self.env,
                                           stdout=stream, stderr=subprocess.STDOUT,
                                           start_new_session=True)
                try:
                    returncode = process.wait(timeout=timeout)
                except (subprocess.TimeoutExpired, KeyboardInterrupt) as exc:
                    # Compose/build can spawn children; bound the entire CLI group.
                    os.killpg(process.pid, signal.SIGTERM)
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        os.killpg(process.pid, signal.SIGKILL)
                        process.wait(timeout=5)
                    if isinstance(exc, KeyboardInterrupt):
                        raise
                    problem = f"timed out after {timeout:g}s"
        except OSError as exc:
            problem = str(exc)
        finally:
            if output.exists():
                with output.open("rb") as stream:
                    size = output.stat().st_size
                    stream.seek(max(0, size - TAIL_BYTES))
                    tail = stream.read(TAIL_BYTES).decode("utf-8", errors="replace")
                if self.artifacts:
                    (self.artifacts / output.name).write_text(tail)
                if not capture or problem or returncode:
                    if size > TAIL_BYTES:
                        self.emit(f"[{layer}] showing final {TAIL_BYTES} bytes of command output")
                    if tail:
                        self.emit(tail.rstrip())
        if problem or returncode:
            description = problem or f"exit {returncode}"
            if required:
                raise LayerFailure(f"{layer}: {description}")
            self.emit(f"[{layer}] diagnostic/cleanup command failed: {description}")
            return None
        if capture:
            if output.stat().st_size > 4 * 1024 * 1024:
                raise LayerFailure(f"{layer}: structured output exceeds 4 MiB")
            return output.read_text()
        return ""

    def compose(self, layer, *args, timeout=60, capture=False, required=True):
        return self.command(layer, self.prefix + ["-f", str(self.composefile), "--profile", "harness"] + list(args),
                            timeout, capture, required)

    def health(self, kind, address):
        return {
            "test": ["CMD", "/e2e/probe", "probe", "-kind", kind,
                     "-address", address, "-timeout", "3s"],
            "interval": "5s", "timeout": "5s",
            "retries": math.ceil(self.args.readiness_timeout / 5),
            "start_period": "10s",
        }

    def prepare(self):
        self.command("prerequisite", ["docker", "compose", "version"], timeout=30)
        raw = self.command("configuration", self.prefix + ["-f", str(self.repo / "docker-compose.yml"),
                           "--profile", "init", "config", "--format", "json"], capture=True)
        # Compose warnings precede JSON on stderr. Preserve them separately rather
        # than making warnings indistinguishable from a malformed model.
        start = raw.find("{")
        if start < 0:
            raise LayerFailure("configuration: Compose did not return a JSON model")
        if raw[:start].strip():
            self.emit(raw[:start].strip())
        try:
            model = json.loads(raw[start:])
        except json.JSONDecodeError as exc:
            raise LayerFailure(f"configuration: invalid Compose JSON: {exc}") from exc
        model["name"] = self.project
        services = model["services"]
        for name in OPTIONAL:
            services.pop(name, None)
        missing = APPLICATIONS.keys() - services.keys()
        if missing:
            raise LayerFailure(f"configuration: missing business services: {sorted(missing)}")
        for name, service in services.items():
            for key in ("container_name", "ports", "restart"):
                service.pop(key, None)
            if service.get("build"):
                service["image"] = f"{self.project}-{name}:e2e"
                service["pull_policy"] = "never"
            for dependency in list(service.get("depends_on", {})):
                if dependency not in services:
                    service["depends_on"].pop(dependency)
            health = service.get("healthcheck")
            if health:
                health["interval"] = "5s"
                health["retries"] = math.ceil(self.args.readiness_timeout / 5)
            for volume in service.get("volumes", []):
                if volume.get("type") == "bind" and not Path(volume["source"]).exists():
                    raise LayerFailure(f"configuration: missing bind source for {name}: {volume['source']}")
        # config expands resource names from its project name. Explicitly remove
        # those names so every volume/network belongs to this unique project.
        for section in ("volumes", "networks"):
            for resource in model.get(section, {}).values():
                resource.pop("name", None)
                resource.pop("external", None)
        model.setdefault("volumes", {})["e2e_probe"] = {}
        probe_mount = {"type": "volume", "source": "e2e_probe", "target": "/e2e", "read_only": True}
        for name, (directory, filename, kind, port, role) in APPLICATIONS.items():
            if not (self.repo / "app" / directory / "etc" / filename).is_file():
                raise LayerFailure(f"configuration: {name} config app/{directory}/etc/{filename} does not exist")
            service = services[name]
            service["command"] = ["-f", "/app/etc/" + filename] + list(role)
            service["hostname"] = name
            service.setdefault("volumes", []).append(copy.deepcopy(probe_mount))
            address = (f"http://127.0.0.1:{port}/health" if kind == "http"
                       else f"127.0.0.1:{port}")
            service["healthcheck"] = self.health(kind, address)
        # Both replicas share one Kafka delivery group; each owns its unique
        # instance subscription and connection registrations.
        for key, value in {
            "REALTIME_HEARTBEAT_INTERVAL": "1", "REALTIME_REGISTRY_TTL_SECONDS": "6",
            "REALTIME_READINESS_DELAY_SECONDS": "2", "REALTIME_DRAIN_SECONDS": "6",
            "REALTIME_WRITE_TIMEOUT_SECONDS": "2", "REALTIME_INSTANCE_ID": "realtime-a",
        }.items():
            services["realtime-service"]["environment"][key] = value
        services["realtime-b"] = copy.deepcopy(services["realtime-service"])
        services["realtime-b"].pop("build", None)
        services["realtime-b"]["hostname"] = "realtime-b"
        services["realtime-b"]["environment"]["REALTIME_INSTANCE_ID"] = "realtime-b"
        networks_a = services["realtime-service"].setdefault("networks", {})
        networks_a["default"] = networks_a.get("default") or {}
        networks_a["default"].setdefault("aliases", []).append("realtime-a")
        for name, address in (("otel-collector", "http://127.0.0.1:13133/"),
                              ("jaeger", "http://127.0.0.1:14269/")):
            services[name].setdefault("volumes", []).append(copy.deepcopy(probe_mount))
            services[name]["healthcheck"] = self.health("http", address)
        services["otel-collector"]["depends_on"]["jaeger"]["condition"] = "service_healthy"
        # External-provider fixture: a real HTTP/SSE server standing in for the
        # OpenAI-compatible and MCP endpoints AIM would otherwise call. AIM services
        # and middleware stay real; only the third-party provider is simulated.
        services["e2e-provider"] = {
            "build": {"context": str(self.repo), "dockerfile": "tests/e2e/provider/Dockerfile"},
            "image": f"{self.project}-provider:e2e", "pull_policy": "never",
            "profiles": ["harness"], "hostname": "e2e-provider",
        }
        services["e2e-provider"].setdefault("volumes", []).append(copy.deepcopy(probe_mount))
        services["e2e-provider"]["healthcheck"] = self.health("http", "http://127.0.0.1:8099/health")
        # Keep real middleware, using bounded Java heaps on developer/CI machines.
        services["kafka"]["environment"]["KAFKA_HEAP_OPTS"] = "-Xmx512m -Xms256m"
        services["neo4j"]["environment"].update({
            "NEO4J_dbms_memory_pagecache_size": "256m",
            "NEO4J_dbms_memory_heap_initial__size": "256m",
            "NEO4J_dbms_memory_heap_max__size": "512m",
        })
        self.infrastructure = sorted(set(services) - set(APPLICATIONS) -
                                     {"realtime-b", "init-kafka-topics"})
        for name in self.infrastructure:
            if not services[name].get("healthcheck") or services[name]["healthcheck"].get("disable"):
                raise LayerFailure(f"configuration: middleware {name} has no real readiness check")
        dependencies = {
            "llm-gateway": ["user-service"],
            "message-service": ["user-service", "bot-service"],
            "knowledge-base": ["llm-gateway"],
            "realtime-service": ["message-service", "bot-service"],
            "bot-runtime": ["llm-gateway", "message-service", "knowledge-base", "bot-service",
                            "user-service"],
            "gateway": sorted(set(APPLICATIONS) - {"gateway"}) + ["realtime-b"],
        }
        for name in list(APPLICATIONS) + ["realtime-b"]:
            service = services[name]
            needs = service.setdefault("depends_on", {})
            for dependency in self.infrastructure + dependencies.get(name, []):
                needs[dependency] = {"condition": "service_healthy"}
        services["e2e-client"] = {
            "build": {"context": str(self.repo), "dockerfile": "tests/e2e/Dockerfile"},
            "image": f"{self.project}-client:e2e", "pull_policy": "never",
            "profiles": ["harness"], "entrypoint": ["/e2e"],
        }
        self.control = self.directory / "control"
        self.control.mkdir(mode=0o777)
        self.control.chmod(0o777)
        services["e2e-client"]["volumes"] = [
            {"type": "bind", "source": str(self.control), "target": "/control"},
        ]
        services["probe-seed"] = {
            "image": "alpine:3.21", "profiles": ["harness"],
            "entrypoint": ["/bin/sh", "-ec"],
            "command": ["cp /seed/probe /e2e/probe && chmod 755 /e2e/probe"],
            "volumes": [
                {"type": "bind", "source": str(self.directory / "probe"),
                 "target": "/seed/probe", "read_only": True},
                {"type": "volume", "source": "e2e_probe", "target": "/e2e"},
            ],
        }
        self.model = model
        self.composefile.write_text(json.dumps(model, indent=2) + "\n")
        self.compose_written = True
        if self.artifacts:
            shutil.copyfile(self.composefile, self.artifacts / "compose.json")
        self.emit(f"[isolation] project={self.project}; {len(APPLICATIONS)} business services + realtime-b; "
                  f"{len(self.infrastructure)} middleware; no published host ports")

    def run(self):
        self.prepare()
        builds = sorted(name for name, service in self.model["services"].items() if service.get("build"))
        # BuildKit bake schedules targets independently of Compose --parallel.
        # Serialize service builds so the full stack fits developer machines.
        for name in builds:
            self.compose("build-" + name, "build", name,
                         timeout=self.args.build_timeout)
        self.compose("pull", "pull", "--ignore-buildable",
                     *self.infrastructure, "probe-seed", timeout=self.args.build_timeout)
        self.compose("probe-create", "create", "--no-build", "e2e-client")
        self.compose("probe-copy", "cp", "e2e-client:/e2e", str(self.directory / "probe"))
        self.compose("probe-seed", "run", "--rm", "--no-deps", "probe-seed")
        self.wait_for("middleware-readiness", self.infrastructure)
        self.compose("kafka-topics-init", "up", "--no-build", "--no-deps", "--abort-on-container-exit",
                     "--exit-code-from", "init-kafka-topics", "init-kafka-topics",
                     timeout=self.args.readiness_timeout)
        self.wait_for("application-readiness", sorted(APPLICATIONS) + ["realtime-b"])
        scenario = ["run", "-gateway", "http://gateway:8080",
                    "-realtime-a", "ws://realtime-a:8081/ws",
                    "-realtime-b", "ws://realtime-b:8081/ws",
                    "-timeout", f"{self.args.timeout:g}s",
                    "-provider", "http://e2e-provider:8099",
                    "-ingest-timeout", "10m", "-query-deadline", "5s"]
        scenario.extend(["-scenario", self.args.scenario])
        if self.args.cross_instance:
            scenario.append("-cross-instance")
        # The client's timeout is per interaction, not an overall scene budget.
        if self.args.scenario == "stage-p6":
            self.lifecycle_acceptance(scenario)
        else:
            self.compose("acceptance", "run", "--rm", "--no-deps", "e2e-client", *scenario,
                         timeout=max(180, self.args.timeout * 100))
        self.emit("[acceptance] PASS")

    def wait_for(self, layer, services):
        self.compose(layer, "up", "--detach", "--no-build", "--wait", "--wait-timeout",
                     str(self.args.readiness_timeout), *services,
                     timeout=self.args.readiness_timeout + 30)
        raw = self.compose(layer + "-states", "ps", "--all", "--format", "json", capture=True)
        states = parse_states(raw)
        by_service = {state.get("Service"): state for state in states}
        for service in services:
            state = by_service.get(service, {})
            if state.get("State") != "running" or state.get("Health") != "healthy":
                raise LayerFailure(f"{layer}: {service}: state={state.get('State', 'missing')} "
                                   f"health={state.get('Health', 'missing')}")
        self.emit(f"[{layer}] all {len(services)} services healthy")

    def lifecycle_acceptance(self, scenario):
        self.model["services"]["e2e-client"]["command"] = scenario + ["-control-dir", "/control"]
        self.composefile.write_text(json.dumps(self.model, indent=2) + "\n")
        if self.artifacts:
            shutil.copyfile(self.composefile, self.artifacts / "compose.json")
        self.compose("p6-client-start", "up", "--detach", "--no-build", "--no-deps", "e2e-client")
        for action in ("kill-b", "restart-b", "feedback-kill-b", "feedback-restart-b", "drain-b", "drain-exited"):
            self.await_checkpoint(action)
            if action == "kill-b":
                self.compose("p6-force-kill", "kill", "--signal", "SIGKILL", "realtime-b")
                # Route expiry is observable via authenticated WS presence.query,
                # not Redis implementation keys. Wait beyond the configured TTL.
                time.sleep(8)
            elif action == "feedback-kill-b":
                self.compose("p6-feedback-kill", "kill", "--signal", "SIGKILL", "realtime-b")
            elif action == "drain-b":
                self.compose("p6-graceful-signal", "kill", "--signal", "SIGTERM", "realtime-b")
            else:
                if action == "feedback-restart-b":
                    self.compose("p6-feedback-evidence", "logs", "--no-color", "--timestamps", "--tail", "100", "realtime-service")
                if action == "drain-exited":
                    self.await_exit("realtime-b", 20)
                    self.compose("p6-drain-evidence", "logs", "--no-color", "--timestamps", "--tail", "100", "realtime-b")
                self.compose("p6-restart", "up", "--detach", "--no-build", "--no-deps", "--wait",
                             "--wait-timeout", str(self.args.readiness_timeout), "realtime-b",
                             timeout=self.args.readiness_timeout + 30)
            (self.control / (action + ".done")).write_text("done\n")
        self.await_exit("e2e-client", max(180, self.args.timeout * 20), require_success=True)
        self.compose("p6-client-evidence", "logs", "--no-color", "--timestamps", "e2e-client")
        self.compose("p6-realtime-evidence", "logs", "--no-color", "--timestamps", "--tail", "100", "realtime-service", "realtime-b")
        self.wait_for("p6-restored-readiness", sorted(APPLICATIONS) + ["realtime-b"])

    def await_checkpoint(self, action):
        deadline = time.monotonic() + max(180, self.args.timeout * 50)
        next_state = 0
        while time.monotonic() < deadline:
            if (self.control / (action + ".request")).is_file():
                self.emit(f"[p6-lifecycle] client checkpoint={action}")
                if self.artifacts:
                    (self.artifacts / (action + ".request")).write_text("ready\n")
                return
            if time.monotonic() >= next_state:
                self.assert_client_running()
                next_state = time.monotonic() + 5
            time.sleep(.1)
        raise LayerFailure(f"p6-lifecycle: client never reached {action}")

    def assert_client_running(self):
        states = parse_states(self.compose("p6-client-state", "ps", "--all", "--format", "json", capture=True))
        state = next((row for row in states if row.get("Service") == "e2e-client"), {})
        if state.get("State") != "running":
            self.compose("p6-client-failure", "logs", "--no-color", "e2e-client", required=False)
            raise LayerFailure(f"p6-lifecycle: client exited before checkpoint, exit={state.get('ExitCode')}")

    def await_exit(self, service, seconds, require_success=False):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            states = parse_states(self.compose("p6-exit-state", "ps", "--all", "--format", "json", capture=True))
            state = next((row for row in states if row.get("Service") == service), {})
            if state.get("State") == "exited":
                if require_success and state.get("ExitCode") != 0:
                    self.compose("p6-client-failure", "logs", "--no-color", service, required=False)
                    raise LayerFailure(f"p6-lifecycle: {service} exit={state.get('ExitCode')}")
                self.emit(f"[p6-lifecycle] {service} exited with code={state.get('ExitCode')}")
                return
            time.sleep(.5)
        raise LayerFailure(f"p6-lifecycle: {service} did not exit within {seconds:g}s")

    def diagnose(self):
        if not self.compose_written:
            return
        raw = self.compose("diagnostic-states", "ps", "--all", "--format", "json",
                           timeout=30, capture=True, required=False)
        if raw:
            try:
                for state in parse_states(raw):
                    self.emit(f"[diagnostic] service={state.get('Service')} state={state.get('State')} "
                              f"health={state.get('Health') or 'none'} exit={state.get('ExitCode')}")
                    identifier = state.get("ID")
                    if identifier and state.get("Health") not in ("", None, "healthy"):
                        self.command("diagnostic-health", ["docker", "inspect", "--format",
                                     "{{json .State.Health}}", identifier], timeout=15, required=False)
            except (ValueError, TypeError) as exc:
                self.emit(f"[diagnostic] cannot decode container states: {exc}")
        for service in sorted(self.model["services"]):
            self.compose("diagnostic-logs-" + service, "logs", "--no-color", "--timestamps",
                         "--tail", "60", service, timeout=15, required=False)

    def cleanup(self):
        if self.compose_written:
            self.compose("cleanup", "down", "--volumes", "--remove-orphans",
                         "--timeout", "10", timeout=120)
        if self.artifacts:
            self.emit(f"[artifacts] bounded logs and resolved model: {self.artifacts}")


def parse_states(raw):
    raw = raw.strip()
    if not raw:
        return []
    if raw.startswith("["):
        return json.loads(raw)
    # Compose v2 versions differ between a JSON array and newline-delimited objects.
    return [json.loads(line) for line in raw.splitlines() if line.strip()]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cross-instance", action="store_true", help="also require real A/B delivery")
    parser.add_argument("--scenario", choices=("all", "relationships", "stage-p3", "conversations", "stage-p4", "conversation-unread", "same-instance-a", "same-instance-b", "cross-instance", "bot-runtime", "knowledge-ingest", "stage-p5", "stage-p6"),
                        default="all", help="select an acceptance scenario; default keeps both-replica coverage")
    parser.add_argument("--timeout", type=duration, default=20, help="per client interaction, e.g. 20s")
    parser.add_argument("--readiness-timeout", type=int, default=300, help="seconds per readiness layer")
    parser.add_argument("--build-timeout", type=int, default=1800, help="seconds per build/pull command")
    parser.add_argument("--artifacts", type=Path, help="preserve bounded logs and resolved Compose model under this directory")
    args = parser.parse_args()
    if args.readiness_timeout <= 0 or args.build_timeout <= 0:
        parser.error("readiness/build timeouts must be positive")
    result = 0
    runner = None
    interrupted_signal = signal.SIGINT

    def interrupt(signum, _frame):
        nonlocal interrupted_signal
        interrupted_signal = signum
        raise KeyboardInterrupt

    previous_term = signal.signal(signal.SIGTERM, interrupt)
    # TemporaryDirectory is removed even on SIGINT/SIGTERM; a project cleanup failure is
    # reported as failure rather than silently claiming an isolated successful run.
    with tempfile.TemporaryDirectory(prefix="aim-e2e-") as directory:
        try:
            runner = Runner(args, directory)
            runner.run()
        except KeyboardInterrupt:
            result = 128 + interrupted_signal
            print("[interrupt] cancelled; collecting diagnostics and removing project", file=sys.stderr)
        except (LayerFailure, OSError, ValueError, KeyError, TypeError) as exc:
            result = 1
            print(f"[failure] {exc}", file=sys.stderr)
        finally:
            # Repeated cancellation must not interrupt the teardown itself.
            previous = signal.signal(signal.SIGINT, signal.SIG_IGN)
            signal.signal(signal.SIGTERM, signal.SIG_IGN)
            try:
                if runner:
                    if result:
                        try:
                            runner.diagnose()
                        except (LayerFailure, OSError, ValueError) as exc:
                            print(f"[diagnostic] {exc}", file=sys.stderr)
                    try:
                        runner.cleanup()
                    except (LayerFailure, OSError) as exc:
                        result = result or 1
                        print(f"[cleanup-failure] {exc}; project={runner.project}", file=sys.stderr)
            finally:
                signal.signal(signal.SIGINT, previous)
                signal.signal(signal.SIGTERM, previous_term)
    return result


if __name__ == "__main__":
    sys.exit(main())
