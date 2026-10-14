"""Offline regression tests of model policy and stop/event handling."""
import importlib.util
import json
from pathlib import Path
import sys
import unittest

HERE = Path(__file__).parent
sys.path.insert(0, str(HERE))
spec = importlib.util.spec_from_file_location("hardening_driver", HERE / "hardening_driver.py")
assert spec is not None and spec.loader is not None
driver = importlib.util.module_from_spec(spec)
spec.loader.exec_module(driver)


class HardeningDriverTests(unittest.TestCase):
    def test_only_owner_model_can_be_selected(self):
        args = driver.cmd()
        self.assertEqual(args[args.index("--model") + 1], "gpt-6.1-sol")
        self.assertEqual(args[args.index("--effort") + 1], "xhigh")
        self.assertNotIn("--fallback-model", args)
        agents = json.loads(args[args.index("--agents") + 1])
        self.assertEqual(len(agents), 2)
        self.assertTrue(all(a["model"] == "gpt-6.1-sol" for a in agents.values()))

    def test_result_event_does_not_depend_on_json_key_order(self):
        raw = '{"duration_ms": 10, "subtype": "success", "type": "result", "result": "ok"}'
        self.assertEqual(driver.parse_event(raw)["type"], "result")

    def test_warnings_are_not_parsed_as_events(self):
        self.assertIsNone(driver.parse_event("warning: connector unavailable"))
        self.assertIsNone(driver.parse_event("[]"))

    def test_historical_done_does_not_stop_new_phase(self):
        self.assertEqual(driver.DONE.name, "HARDENING_DONE")
        self.assertTrue(driver.has_open_tasks("- [x] H01\n  - [ ] H02a"))
        self.assertFalse(driver.has_open_tasks("- [x] H01\n- [x] H02\n"))

    def test_one_task_per_session_and_explicit_subagents(self):
        self.assertIn("ONLY the first unchecked", driver.PROMPT)
        self.assertIn("gpt-reproducer", driver.PROMPT)
        self.assertIn("gpt-reviewer", driver.PROMPT)
        self.assertIn("Historical docs/agent/DONE", driver.PROMPT)


if __name__ == "__main__":
    unittest.main()
