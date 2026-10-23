# Deferred hardening scope

Scope moved out of the ordered plan under the owner's 2026-10-22 time-box rule: work that is precision,
performance, generated-code size or other refinement where programs already behave correctly. Nothing
here is done; each entry names the later task that owns it and must be listed again in HARDENING_DONE.

## H09b5c
- **What remains:** Retain parameter/returned containers, default/rest/terminal-value and other destructured containers, numeric/computed keys and explicit pattern-owner annotations, nonliteral nested origins, broader caller/return flow beyond accepted children where provable, remaining receiver/source precision and necessary m4 measurements. No scope is dropped; uncertain flows retain compatible instances.
- **Why deferred:** after 14 nested slices (H09b5c1 … H09b5c2b2b2b2b2b2b2a, D-201–D-215, all committed with
  green local suites) the remainder kept being re-split into ever-narrower flow-precision cases. Every
  uncertain flow already keeps a compatible instance, so the remaining cost is C size and build time
  (C172: esc_150x50 92,388 lines / 21.6 s; methods_60 93,741 lines / 49 s), not wrong results.
- **Owner:** H25 (escape analysis / flow) and H27 (code generation), with the m4 matrix as the measurement.
- **Evidence:** docs/agent/hardening-evidence/h09b5c*.md, RED test `driver/speculative_instances_test.go`.
