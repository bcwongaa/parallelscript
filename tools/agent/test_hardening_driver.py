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
        self.assertEqual(args[args.index("--model") + 1], "claude-opus-5-5")
        self.assertEqual(args[args.index("--effort") + 1], "xhigh")
        self.assertNotIn("--fallback-model", args)
        agents = json.loads(args[args.index("--agents") + 1])
        self.assertEqual(len(agents), 2)
        self.assertTrue(all(a["model"] == "claude-opus-5-5" for a in agents.values()))

    def test_result_event_does_not_depend_on_json_key_order(self):
        raw = '{"duration_ms": 10, "subtype": "success", "type": "result", "result": "ok"}'
        self.assertEqual(driver.parse_event(raw)["type"], "result")

    def test_warnings_are_not_parsed_as_events(self):
        self.assertIsNone(driver.parse_event("warning: connector unavailable"))
        self.assertIsNone(driver.parse_event("[]"))

    def test_synthetic_cli_error_is_not_an_inference_model(self):
        event = {"type": "assistant", "message": {"model": "<synthetic>", "content": [
            {"type": "text", "text": "API Error: This content was flagged for possible cybersecurity risk."}]}}
        self.assertIsNone(driver.unapproved_model(event))
        self.assertTrue(driver.is_content_refusal(event))

    def test_actual_different_model_still_stops_run(self):
        event = {"type": "assistant", "message": {"model": "gpt-6.1-sol"}}
        self.assertEqual(driver.unapproved_model(event), "gpt-6.1-sol")
        self.assertIsNone(driver.unapproved_model({"type": "assistant", "message": {"model": "claude-opus-5-5"}}))

    def test_content_refusal_is_separate_from_other_synthetic_errors(self):
        event = {"type": "assistant", "message": {"model": "<synthetic>", "content": [
            {"type": "text", "text": "API Error: temporarily unavailable"}]}}
        self.assertIsNone(driver.unapproved_model(event))
        self.assertFalse(driver.is_content_refusal(event))

    def test_historical_done_does_not_stop_new_phase(self):
        self.assertEqual(driver.DONE.name, "HARDENING_DONE")
        self.assertTrue(driver.has_open_tasks("- [x] H01\n  - [ ] H02a"))
        self.assertFalse(driver.has_open_tasks("- [x] H01\n- [x] H02\n"))

    def test_one_task_per_session_and_explicit_subagents(self):
        self.assertIn("ONLY the first unchecked, unblocked", driver.PROMPT)
        self.assertIn("opus-reproducer", driver.PROMPT)
        self.assertIn("opus-reviewer", driver.PROMPT)
        self.assertFalse(driver.VIA_PROXY)
        self.assertNotIn("gpt-6.1-sol", driver.PROMPT)
        self.assertIn("Historical docs/agent/DONE", driver.PROMPT)


if __name__ == "__main__":
    unittest.main()
