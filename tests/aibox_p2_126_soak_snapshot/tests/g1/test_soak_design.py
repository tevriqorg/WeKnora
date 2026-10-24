"""Design-gate regression: required test matrix must not silently shrink."""
import hashlib
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
PLAN = ROOT / "docs/P2_126_15_DAY_REAL_DATA_SOAK_PLAN.md"
MATRIX = ROOT / "deploy/aibox-0.8/vela-uat/soak/cases.json"
LEARNING = ROOT / "deploy/aibox-0.8/vela-uat/soak/learning_rules.json"
# Frozen initial acceptance matrix; approved extensions are separate append-only regressions.
FROZEN_MATRIX_SHA256 = "c3b3d89b96f1008e171831780b447bbc8d56e1f218d4651236ad7b9fb962b2e1"


class SoakDesignContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cases = json.loads(MATRIX.read_text(encoding="utf-8"))["cases"]
        cls.plan = PLAN.read_text(encoding="utf-8")
        cls.learning = json.loads(LEARNING.read_text(encoding="utf-8"))

    def test_initial_matrix_is_immutable_during_trial(self):
        self.assertEqual(hashlib.sha256(MATRIX.read_bytes()).hexdigest(),FROZEN_MATRIX_SHA256)

    def test_matrix_has_required_categories(self):
        modes = {x["mode"] for x in self.cases}
        self.assertTrue({"at_rest","transit","continuous","kernel","temporal","security","recovery","observability","learning","lifecycle"} <= modes)
        self.assertGreaterEqual(len(self.cases), 30)
        self.assertEqual(len({c["id"] for c in self.cases}), len(self.cases))

    def test_each_case_has_verifiable_assertions(self):
        required={"id","layer","when","severity","required","oracle","assertion","evidence_required"}
        for row in self.cases:
            with self.subTest(case=row.get("id")):
                self.assertTrue(required <= row.keys())
                self.assertTrue(row["required"])
                self.assertGreater(len(row["assertion"]), 25)
                self.assertIn(row["severity"], ("P0","P1","P2","P3"))
                self.assertIn(row["layer"], ("live","clone","host_read_only","review"))
                self.assertIn("oracle_hash",row["evidence_required"])
                self.assertIn("timestamp_utc",row["evidence_required"])

    def test_live_semantics_and_high_risk_clone_safety(self):
        for m in ("at_rest","transit","continuous","kernel","temporal"):
            self.assertTrue(any(x["mode"]==m and x["layer"]=="live" for x in self.cases),m)
        self.assertTrue(all(x["layer"]=="clone" for x in self.cases if x["mode"]=="security"))
        self.assertTrue(all(x["layer"]=="clone" for x in self.cases if x["mode"]=="recovery"))

    def test_no_false_soak_and_required_bug_learning(self):
        for phrase in ("Gate 0","360","REQUALIFY","DETECTED","TARGETED_PASS","REGRESSION_PASS","LEARNING_COMMITTED","oracle","#126","P0","P1","GitHub Actions"):
            with self.subTest(required=phrase):
                self.assertIn(phrase,self.plan)
        for case in ("LR01","LR02","OB02","LN01","LN02"):
            self.assertIn(case,{x["id"] for x in self.cases})


    def test_self_learning_rules_have_no_oracle_shortcut(self):
        rules=self.learning["rules"]
        policy=self.learning["policy"]
        self.assertGreaterEqual(len(rules),10)
        self.assertFalse(policy["auto_modify_original_oracle"])
        self.assertFalse(policy["auto_weaken_assertions"])
        self.assertFalse(policy["destructive_in_live_uat_allowed"])
        self.assertTrue(policy["require_red_before_green"])
        self.assertTrue(policy["require_negative_case"])
        self.assertTrue(policy["require_independent_frozen_oracle"])
        self.assertIn("reviewed_fix_pr",policy["promotion_requires"])
        self.assertIn("targeted_retest",policy["promotion_requires"])
        self.assertIn("canary_live_smoke",policy["promotion_requires"])
        ids={r["id"] for r in self.cases}
        signatures=set()
        for rule in rules:
            self.assertNotIn(rule["signature"],signatures)
            signatures.add(rule["signature"])
            self.assertTrue(rule["only_in_isolated_clone_for_faults"])
            self.assertTrue(rule["required_mutation"])
            self.assertGreaterEqual(len(rule["prioritized_case_ids"]),3)
            self.assertGreaterEqual(len(rule["expansion_axes"]),3)
            self.assertTrue(set(rule["prioritized_case_ids"])<=ids)


    def test_missing_oracle_or_weakened_coverage_is_detected(self):
        # Negative design-gate checks; static matrix must not silently shrink.
        required={r["id"] for r in self.cases}
        self.assertIn("LN01",required)
        self.assertIn("LN02",required)
        self.assertGreaterEqual(len(required),35)
        self.assertTrue(all(r["oracle"] and "oracle_hash" in r["evidence_required"] for r in self.cases))


if __name__=="__main__":
    unittest.main()
