#!/usr/bin/env python3
"""P2 #126 fail-closed evidence, incidents, learning and 360-slot evaluation.

No test runner or repair agent has implicit access to production assets.
Never auto-promote a long trial from a timestamp, a green L0, or mock CI.
"""
from __future__ import annotations
import argparse
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import sys
from gate_guard import FROZEN_MATRIX_SHA256, validate_gate

HERE = Path(__file__).resolve().parent
UTC = dt.timezone.utc
INCIDENT_STATES = [
    "DETECTED", "TRIAGED", "REPRODUCED", "FIX_IN_PR",
    "CI_VERIFIED", "CANARY_DEPLOYED", "TARGETED_PASS",
    "REGRESSION_PASS", "LEARNING_COMMITTED", "VERIFIED_CLOSED"
]
VALID_STATES = set(INCIDENT_STATES) | {"BLOCKED", "REOPENED", "QUARANTINED"}
EVIDENCE_FIELDS = ("p2_sha", "weknora_sha", "oracle_hash", "timestamp_utc", "case_id")


def now_utc():
    return dt.datetime.now(UTC).isoformat(timespec="seconds")


def append_event(path: Path, value: dict):
    """Atomic per-record append with file sync; never replace old verdicts."""
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(path.parent, 0o700)
    fd = os.open(path, os.O_CREAT | os.O_APPEND | os.O_WRONLY, 0o600)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX)
        os.write(fd, (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode())
        os.fsync(fd)
    finally:
        fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)


def read_events(path):
    if not path.exists():
        return []
    with path.open(encoding="utf-8") as f:
        result = []
        for i, line in enumerate(f, 1):
            try:
                x = json.loads(line)
                if not isinstance(x, dict):
                    raise ValueError("non-object record")
            except (ValueError, json.JSONDecodeError) as e:
                raise ValueError(f"{path.name}: invalid record {i}: {e}") from e
            result.append(x)
        return result


def load_rules(base=HERE):
    cases = json.loads((base / "cases.json").read_text())["cases"]
    rules = json.loads((base / "learning_rules.json").read_text())
    if hashlib.sha256((base / "cases.json").read_bytes()).hexdigest() != FROZEN_MATRIX_SHA256:
        raise ValueError("frozen case matrix corrupted or changed")
    return {x["id"]: x for x in cases}, rules["rules"]


def sanitized_label(s: str, limit=96) -> str:
    if not isinstance(s, str) or not s or len(s) > limit:
        raise ValueError("missing/invalid label")
    if any(c not in "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.:-" for c in s):
        raise ValueError("label contains unsafe data")
    return s


def failing_incident(state_dir: Path, *, case_id, signature, source_id, metadata=None, base=HERE):
    """Create deduplicated incident, targeted test plan; no secret/error body."""
    cases, rules = load_rules(base)
    case = cases.get(sanitized_label(case_id))
    if case is None:
        raise ValueError("unknown mandatory case")
    signature = sanitized_label(signature)
    source_id = sanitized_label(source_id)
    rule = next((r for r in rules if r["signature"] == signature), None)
    if rule is None:
        # Unknown failure is never silently dropped; require triage.
        related = [case_id]
        extras = ["new_signature_requires_review"]
    else:
        related = [case_id] + [x for x in rule["prioritized_case_ids"] if x != case_id]
        extras = rule["expansion_axes"][:6]
    key = f"{case_id}|{signature}|{source_id}"
    incident = "P2-126-" + hashlib.sha256(key.encode()).hexdigest()[:16]
    previous = [e for e in read_events(state_dir / "incidents.jsonl") if e.get("incident_id") == incident]
    seq = len(previous) + 1
    result = {
        "incident_id": incident, "event_seq": seq, "state": "DETECTED" if not previous else "REOPENED",
        "case_id": case_id, "signature": signature, "source_id": source_id,
        "severity": case["severity"], "detected_utc": now_utc(),
        "targeted_cases": sorted(set(related)), "candidate_axes": extras,
        "requires_human_or_agent_fix": True, "auto_close": False,
        "oracle_change_allowed": False, "destructive_repro_allowed": False,
        "trial_must_requalify": case["severity"] in ("P0","P1"),
    }
    if metadata:
        allowed = {"p2_sha", "weknora_sha", "oracle_hash", "test_run_id", "evidence_sha256"}
        if not set(metadata) <= allowed:
            raise ValueError("unreviewed metadata keys could leak sensitive data")
        result["evidence"] = metadata
    append_event(state_dir / "incidents.jsonl", result)
    if seq == 1:
        append_event(state_dir / "case_proposals.jsonl", {
            "incident_id": incident, "created_utc": result["detected_utc"],
            "case_ids": result["targeted_cases"], "mutation_axes": extras,
            "status": "CANDIDATE_REVIEW_REQUIRED",
            "reason": "red-before-green and negative/adjacent cases mandatory",
        })
    return result


def transition_incident(state_dir: Path, incident_id: str, target: str,
                        evidence: dict | None = None):
    """Advance only with independent evidence. Never fabricate repair progress."""
    if target not in VALID_STATES:
        raise ValueError("invalid target state")
    events = [e for e in read_events(state_dir / "incidents.jsonl")
              if e.get("incident_id") == incident_id]
    if not events:
        raise ValueError("unknown incident")
    last = events[-1]
    prior = last["state"]
    if target not in {"BLOCKED", "REOPENED", "QUARANTINED"}:
        if prior == "REOPENED" and target == "TRIAGED":
            pass
        elif prior not in INCIDENT_STATES or INCIDENT_STATES.index(target) != INCIDENT_STATES.index(prior) + 1:
            raise ValueError("state transition skips required repair/retest stage")
        if not evidence or not evidence.get("artifact_sha256") or not evidence.get("evaluator"):
            raise ValueError("independent, fingerprinted artifact required for every promotion")
        if len(evidence["artifact_sha256"]) != 64 or any(x not in "0123456789abcdef" for x in evidence["artifact_sha256"]):
            raise ValueError("invalid artifact digest")
        sanitized_label(evidence["evaluator"])
    event = {"incident_id": incident_id, "event_seq": len(events) + 1,
             "state": target, "from": prior, "timestamp_utc": now_utc(),
             "evidence": evidence or {}, "case_id": last["case_id"],
             "severity": last["severity"], "signature": last["signature"]}
    append_event(state_dir / "incidents.jsonl", event)
    return event


def slot_id(start: dt.datetime, t: dt.datetime):
    return int((t - start).total_seconds() // 3600)


def parse_time(v):
    x = dt.datetime.fromisoformat(v.replace("Z", "+00:00"))
    if x.tzinfo is None:
        raise ValueError("naive timestamp is not allowed")
    return x.astimezone(UTC)


def evaluate(state_dir: Path, *, now=None, gate_key=None, base=HERE):
    """Truthful status; 360 hours alone is never a PASS."""
    now = now or dt.datetime.now(UTC)
    gate, reason = validate_gate(state_dir / "gate.json", key_path=gate_key, now=now)
    if not gate:
        return {"verdict":"BLOCKED","mode":"preflight","reason":reason,
                "observed_health_samples":len(read_events(state_dir/"samples.jsonl"))}
    start = parse_time(gate["start_utc"])
    completed_slots = min(360, max(0, slot_id(start, now)))
    cases, _ = load_rules(base)
    l0_by_slot = set()
    l1_by_slot = set()
    for e in read_events(state_dir / "samples.jsonl"):
        try:
            stamp = parse_time(e["sample_time_utc"])
            slot = slot_id(start, stamp)
            if 0 <= slot < completed_slots and e.get("all_healthy") is True and e.get("mode")=="soak" and e.get("trial_id")==gate["trial_id"]:
                l0_by_slot.add(slot)
        except (KeyError, TypeError, ValueError):
            continue
    for e in read_events(state_dir / "business.jsonl"):
        try:
            stamp = parse_time(e["timestamp_utc"])
            slot = slot_id(start, stamp)
            if 0 <= slot < completed_slots and e.get("trial_id")==gate["trial_id"] and e.get("case_id")=="KB01" and e.get("result")=="PASS" and e.get("oracle_sha256")==gate["oracle_sha256"]:
                l1_by_slot.add(slot)
        except (KeyError, ValueError, TypeError):
            continue
    daily = {}
    for e in read_events(state_dir / "daily.jsonl"):
        try:
            stamp = parse_time(e["timestamp_utc"])
            day = slot_id(start, stamp)//24
            if (e.get("trial_id") == gate["trial_id"] and e.get("result") == "PASS"
                    and 0 <= day < 15 and e.get("oracle_sha256")==gate["oracle_sha256"]
                    and len(set(e.get("formats",[])))>=2 and e.get("replay_pass") is True
                    and e.get("incremental_pass") is True):
                daily[day] = True
        except (KeyError, ValueError, TypeError):
            continue
    passed = set()
    for e in read_events(state_dir / "case_results.jsonl"):
        if (e.get("trial_id")==gate["trial_id"] and e.get("result")=="PASS"
            and e.get("oracle_sha256")==gate["oracle_sha256"] and e.get("evidence_sha256")):
            passed.add(e.get("case_id"))
    missing_cases = sorted(set(cases) - passed)
    missing_l0 = sorted(set(range(completed_slots)) - l0_by_slot)
    missing_l1 = sorted(set(range(completed_slots)) - l1_by_slot)
    unresolved = {}
    for e in read_events(state_dir / "incidents.jsonl"):
        if e.get("incident_id"):
            unresolved[e["incident_id"]] = e
    blocking = sorted(k for k,v in unresolved.items() if v.get("state")!="VERIFIED_CLOSED"
                      and v.get("severity") in ("P0","P1"))
    good = (completed_slots==360 and len(l0_by_slot)==360 and len(l1_by_slot)==360
            and len(daily)==15 and not missing_cases and not blocking)
    return {"mode":"soak","trial_id":gate["trial_id"],
            "verdict":"READY_FOR_INDEPENDENT_REVIEW" if good else "BLOCKED",
            "hours_elapsed":round((now-start).total_seconds()/3600,2),
            "completed_slots":completed_slots,"l0_slots":len(l0_by_slot),
            "l1_slots":len(l1_by_slot),"daily_days":len(daily),
            "missing_l0_slots":missing_l0[:24], "missing_l1_slots":missing_l1[:24],
            "missing_case_ids":missing_cases,"unresolved_p0_p1":blocking,
            "note":"READY_FOR_INDEPENDENT_REVIEW is never an automatic PASS"}


def main():
    p=argparse.ArgumentParser()
    p.add_argument("--state-dir",type=Path,required=True)
    sub=p.add_subparsers(dest="command",required=True)
    sub.add_parser("evaluate")
    v=sub.add_parser("incident")
    v.add_argument("--case-id",required=True);v.add_argument("--signature",required=True)
    v.add_argument("--source-id",required=True)
    t=sub.add_parser("transition")
    t.add_argument("--incident-id",required=True)
    t.add_argument("--state",required=True)
    t.add_argument("--artifact-sha256",required=True)
    t.add_argument("--evaluator",required=True)
    args=p.parse_args()
    args.state_dir.mkdir(parents=True,exist_ok=True,mode=0o700)
    if args.command=="evaluate":
        res=evaluate(args.state_dir)
    elif args.command=="incident":
        res=failing_incident(args.state_dir,case_id=args.case_id,
                             signature=args.signature,source_id=args.source_id)
    else:
        res=transition_incident(args.state_dir,args.incident_id,args.state,
                               {"artifact_sha256":args.artifact_sha256,"evaluator":args.evaluator})
    print(json.dumps(res,sort_keys=True))
    if args.command=="evaluate" and res["verdict"]=="BLOCKED":
        sys.exit(2)


if __name__=="__main__":
    main()
