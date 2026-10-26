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
        self.assertEqual(args[args.index("--max-turns") + 1], "500")
        agents = json.loads(args[args.index("--agents") + 1])
        self.assertEqual(len(agents), 2)
        self.assertTrue(all(a["model"] == "claude-opus-5-5" for a in agents.values()))

    def test_fallback_chain_and_per_model_session(self):
        self.assertEqual([m for m, _ in driver.CHAIN], ["claude-opus-5-5", "gpt-6.1-sol", "grok-4.7"])
        for model, proxy in driver.CHAIN:
            args = driver.cmd(model)
            self.assertEqual(args[args.index("--model") + 1], model)
            self.assertIn(f"running as {model}", args[2])
            self.assertTrue(all(a["model"] == model for a in json.loads(args[args.index("--agents") + 1]).values()))
            e = driver.env(model)
            self.assertEqual("ANTHROPIC_BASE_URL" in e, proxy)
            self.assertEqual(e["CLAUDE_CODE_SUBAGENT_MODEL"], model)
        self.assertIsNone(driver.unapproved_model({"message": {"model": "grok-4.7"}}, "grok-4.7"))
        self.assertEqual(driver.unapproved_model({"message": {"model": "claude-opus-5-5"}}, "grok-4.7"), "claude-opus-5-5")

    def test_rejected_rate_limit_event_sets_cooldown(self):
        ev = {"type": "rate_limit_event", "rate_limit_info": {"status": "rejected", "resetsAt": 1792656000}}
        self.assertEqual(driver.limit_reset(ev), 1792656000.0)
        self.assertIsNone(driver.limit_reset({"type": "rate_limit_event", "rate_limit_info": {"status": "allowed"}}))

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
        self.assertIn("first unchecked ([ ]) unblocked task", driver.PROMPT)
        self.assertIn("Never split a remainder again", driver.PROMPT)
        self.assertFalse(driver.has_open_tasks("- [x] H01\n  - [~] H09b5c deferred\n"))
        self.assertIn("-p 4 -parallel 4", driver.PROMPT)
        self.assertIn("NEVER wait or poll for GitHub CI", driver.PROMPT)
        self.assertNotIn("Verify exact\nSHA CI", driver.PROMPT)
        self.assertEqual(set(driver.AGENTS), {"reproducer", "reviewer"})
        self.assertIn("reproducer and reviewer subagents", driver.PROMPT)
        self.assertFalse(driver.VIA_PROXY)
        self.assertIn("Historical docs/agent/DONE", driver.PROMPT)


class BudgetTests(unittest.TestCase):
    def test_pacing_allows_more_as_the_week_passes(self):
        reset = 1_000_000_000.0
        start = reset - driver.WEEK
        self.assertAlmostEqual(driver.weekly_allowance(reset, start), driver.OPUS_HEADSTART)
        self.assertAlmostEqual(driver.weekly_allowance(reset, reset - driver.WEEK / 2),
                               driver.OPUS_HEADSTART + driver.OPUS_WEEKLY_CAP / 2)
        self.assertEqual(driver.weekly_allowance(reset, reset - 1), driver.OPUS_WEEKLY_CAP)

    def test_budget_blocks_over_pace_and_resumes_later(self):
        reset = 1_000_000_000.0
        now = reset - driver.WEEK + 24 * 3600  # one day in: allowance = 0.10 + 0.70/7 = 0.20
        ok, why, retry = driver.budget_ok({"seven_day": (0.35, reset), "five_hour": (0.1, now + 3600)}, now)
        self.assertFalse(ok)
        self.assertGreater(retry, now)
        self.assertLess(retry, reset)
        ok, _, _ = driver.budget_ok({"seven_day": (0.15, reset), "five_hour": (0.1, now + 3600)}, now)
        self.assertTrue(ok)

    def test_cap_and_five_hour_window(self):
        reset = 1_000_000_000.0
        now = reset - 3600
        ok, _, retry = driver.budget_ok({"seven_day": (0.71, reset)}, now)
        self.assertFalse(ok)
        self.assertGreaterEqual(retry, reset)
        ok, why, _ = driver.budget_ok({"seven_day": (0.2, reset), "five_hour": (0.8, now + 600)}, now)
        self.assertFalse(ok)
        self.assertIn("5-hour", why)

    def test_windows_parsed_from_rate_limit_event(self):
        ev = {"type": "rate_limit_event", "rate_limit_info": {"unifiedWindows": {
            "five_hour": {"utilization": 0.2, "resetsAt": 5}, "seven_day": {"utilization": 0.4, "resetsAt": 9}}}}
        self.assertEqual(driver.windows(ev), {"five_hour": (0.2, 5.0), "seven_day": (0.4, 9.0)})


if __name__ == "__main__":
    unittest.main()
