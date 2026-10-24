#!/usr/bin/env python3
"""Protocol classification unit tests; no Docker, worker or policy execution."""
import copy
import subprocess
import threading
import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch

from manager import ID_LABEL, OWNER_LABEL, Manager


def record():
    return dict(execution_id="fixture", run_id="a" * 64, phase="cleaned", outcome="failed",
                input_sha256="b" * 64, image_id="sha256:" + "c" * 64, worker_sha256="d" * 64,
                validation={"fixture": True}, evidence={"wait": 2}, result=None)


class ValidationFailureClassification(unittest.TestCase):
    def setUp(self):
        self.manager = Manager.__new__(Manager)

    def test_environment_failures_do_not_become_quality_failures(self):
        for outcome in ("failed", "timeout", "stopped", "unknown"):
            with self.subTest(outcome=outcome):
                current = record()
                current["outcome"] = outcome
                self.assertEqual(self.manager.validation_view(current)["failure_class"], "environment_error")

    def test_worker_failure_requires_delivered_input_and_matching_wait(self):
        current = record()
        current["failure_class"] = "worker_failure"
        self.assertEqual(self.manager.validation_view(current)["failure_class"], "environment_error")
        current["evidence"].update(input_delivered=True, attached_exit_code=2)
        self.assertEqual(self.manager.validation_view(current)["failure_class"], "worker_failure")
        current["evidence"]["attached_exit_code"] = 125  # CLI failure is not workload exit evidence.
        self.assertEqual(self.manager.validation_view(current)["failure_class"], "environment_error")

    def test_only_confirmed_worker_resource_boundaries_are_quality_failures(self):
        for outcome in ("timeout", "output_limit"):
            with self.subTest(outcome=outcome):
                current = record()
                current.update(outcome=outcome, failure_class="worker_failure")
                current["evidence"]["input_delivered"] = True
                self.assertEqual(self.manager.validation_view(current)["failure_class"], "environment_error"
                                 if outcome == "timeout" else "worker_failure")
                if outcome == "timeout":
                    current["evidence"]["workload_timeout"] = True
                self.assertEqual(self.manager.validation_view(current)["failure_class"], "worker_failure")

    def test_classification_does_not_expose_diagnostics(self):
        current = record()
        current["error"] = "private management diagnostic"
        current["evidence"]["stderr"] = "private worker diagnostic"
        view = self.manager.validation_view(current)
        self.assertNotIn("error", view)
        self.assertNotIn("stderr", view)
        self.assertNotIn("evidence", view)

    def test_create_failure_remains_environment_error_after_cleanup(self):
        current = record()
        current.update(phase="intent", outcome="unknown", deadline=10**12, stop_requested=False,
                       limits={"cpu_millis": 500, "memory_bytes": 134217728, "pids": 32,
                               "workspace_bytes": 8388608, "tmp_bytes": 2097152},
                       container_name="fixture-container")
        self.manager.lock = threading.RLock()
        self.manager.owner = "fixture-owner"
        self.manager.policy = {"image_id": current["image_id"]}
        self.manager.active = {}
        self.manager._docker = Mock(side_effect=RuntimeError("private create error"))
        self.manager._recovery_checkpoint = Mock()
        self.manager._cleanup = Mock(side_effect=lambda item: item.update(phase="cleaned"))
        original = copy.deepcopy(current)
        self.manager._execute(current, "")
        self.assertEqual(current["outcome"], "failed")
        self.assertEqual(self.manager.validation_view(current)["failure_class"], "environment_error")
        self.assertEqual(original["input_sha256"], current["input_sha256"])

    def execute_timeout(self, trigger, worker_running=False, overall_seconds=60, observation_error=False):
        current = record()
        current.update(phase="intent", outcome="unknown", deadline=overall_seconds, stop_requested=False,
                       operation="csv-v1", container_name="fixture-container",
                       limits={"cpu_millis": 500, "memory_bytes": 134217728, "pids": 32,
                               "workspace_bytes": 8388608, "tmp_bytes": 2097152, "step_seconds": 5,
                               "output_bytes": 1048576, "output_files": 1, "log_bytes": 65536})
        current["evidence"] = {}
        identity = "e" * 64
        snapshot = dict(Id=identity, Name="/fixture-container", State={"Running": True},
                        Config={"Labels": {OWNER_LABEL: "fixture-owner", ID_LABEL: "fixture"}})
        expired_snapshot = copy.deepcopy(snapshot)
        expired_snapshot["State"]["Running"] = worker_running
        self.manager.lock = threading.RLock()
        self.manager.owner = "fixture-owner"
        self.manager.policy = {"image_id": current["image_id"]}
        self.manager.env = {}
        self.manager.halt = threading.Event()
        self.manager.active = {"fixture": True}
        self.manager._docker = Mock(return_value=(0, identity.encode(), b""))
        self.manager._inspect = Mock(side_effect=[snapshot, RuntimeError("private inspect error")
                                                  if observation_error else expired_snapshot])
        self.manager._running_evidence = Mock()
        self.manager._save = Mock()
        self.manager._recovery_checkpoint = Mock()

        def cleanup(item):
            item["phase"] = "cleaned"
            item["evidence"].update(wait=137 if worker_running else 0, stop_confirmed=True,
                                    removed=True, resources_absent=True)

        self.manager._cleanup = Mock(side_effect=cleanup)
        process = Mock()
        process.poll.return_value = None
        for stream in (process.stdin, process.stdout, process.stderr):
            stream.closed = False
        process.wait.side_effect = ([subprocess.TimeoutExpired("docker start", 5), 0]
                                    if trigger == "attach_wait" else [0])
        selector = Mock()
        registrations = {}
        selector.register.side_effect = lambda stream, events, data: registrations.update(
            {stream: SimpleNamespace(fileobj=stream, data=data)})
        selector.unregister.side_effect = registrations.pop
        selector.get_map.side_effect = lambda: registrations
        clock = {"now": 0}

        def select_events(_):
            if process.stdin in registrations:
                return [(registrations[process.stdin], 0)]
            clock["now"] = min(6, overall_seconds)
            if trigger == "attach_wait":
                return [(key, 0) for key in registrations.values()]
            return []

        selector.select.side_effect = select_events
        with patch("manager.subprocess.Popen", return_value=process), \
                patch("manager.selectors.DefaultSelector", return_value=selector), \
                patch("manager.os.set_blocking"), patch("manager.os.write", side_effect=lambda fd, data: len(data)), \
                patch("manager.os.read", return_value=b""), \
                patch("manager.time.time", return_value=0), \
                patch("manager.time.monotonic", side_effect=lambda: clock["now"]):
            self.manager._execute(current, "")
        self.assertTrue(current["evidence"]["input_delivered"])
        self.assertEqual(current["outcome"], "timeout")
        self.manager._cleanup.assert_called_once_with(current)
        process.kill.assert_called_once_with()
        selector.close.assert_called_once_with()
        self.assertNotIn("fixture", self.manager.active)
        view = self.manager.validation_view(current)
        self.assertTrue(view["stop_confirmed"] and view["removed"] and view["resources_absent"])
        return current, view

    def test_attach_wait_timeout_with_worker_exit_zero_is_environment_error(self):
        current, view = self.execute_timeout("attach_wait")
        self.assertEqual(view["wait"], 0)
        self.assertEqual(view["failure_class"], "environment_error")
        self.assertNotIn("workload_timeout", current["evidence"])

    def test_pipe_timeout_with_worker_exit_zero_is_environment_error(self):
        current, view = self.execute_timeout("pipes")
        self.assertEqual(view["failure_class"], "environment_error")
        self.assertNotIn("workload_timeout", current["evidence"])

    def test_observed_running_worker_after_step_budget_is_worker_failure(self):
        for trigger in ("pipes", "attach_wait"):
            with self.subTest(trigger=trigger):
                current, view = self.execute_timeout(trigger, worker_running=True)
                self.assertEqual(view["failure_class"], "worker_failure")
                self.assertTrue(current["evidence"]["workload_timeout"])

    def test_manager_deadline_before_full_workload_budget_is_environment_error(self):
        current, view = self.execute_timeout("pipes", worker_running=True, overall_seconds=3)
        self.assertEqual(view["failure_class"], "environment_error")
        self.assertNotIn("workload_timeout", current["evidence"])

    def test_unknown_worker_state_after_transport_timeout_is_environment_error(self):
        current, view = self.execute_timeout("attach_wait", observation_error=True)
        self.assertEqual(view["failure_class"], "environment_error")
        self.assertNotIn("workload_timeout", current["evidence"])


if __name__ == "__main__":
    unittest.main()
