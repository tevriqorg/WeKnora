#!/usr/bin/env python3
"""Independent missed-slot detector; never treat non-delivery as sent alert."""
import argparse
import datetime as dt
import json
from pathlib import Path
import subprocess
import sys

from soak_engine import read_events, failing_incident, append_event, now_utc, evaluate


def latest_sample_fresh(state_dir, now=None, max_age_minutes=100):
    now=now or dt.datetime.now(dt.timezone.utc)
    snapshots=read_events(state_dir/"samples.jsonl")
    if not snapshots:
        return False,"missing_samples"
    try:
        t=dt.datetime.fromisoformat(snapshots[-1]["sample_time_utc"].replace("Z","+00:00"))
        age=(now-t).total_seconds()/60
        if t.tzinfo is None or age < -5 or age > max_age_minutes:
            return False,"stale_samples"
        if not snapshots[-1].get("all_healthy"):
            return False,"unhealthy_samples"
        return True,"fresh"
    except (KeyError,TypeError,ValueError):
        return False,"unreadable_samples"


def check(state_dir,now=None):
    ok,reason=latest_sample_fresh(state_dir,now)
    trial=evaluate(state_dir,now=now)
    if not ok:
        incident=failing_incident(state_dir,case_id="OB02",signature="monitor_blindness",source_id="l0-sampler")
        return {"result":"FAIL","reason":reason,"incident_id":incident["incident_id"],
                "mode":trial["mode"]}
    if trial["mode"]=="soak" and (trial.get("missing_l0_slots") or trial.get("missing_l1_slots")):
        incident=failing_incident(state_dir,case_id="OB02",signature="monitor_blindness",source_id="soak-coverage")
        return {"result":"FAIL","reason":"business_or_health_slot_missing",
                "incident_id":incident["incident_id"],"mode":"soak"}
    return {"result":"PASS","mode":trial["mode"],"reason":"read_only_watchdog_no_gate_promotion"}


def notify_issue_once(state_dir,incident_id):
    """Explicitly enabled auth action; if missing/failing report NOT_DELIVERED."""
    already={e["incident_id"] for e in read_events(state_dir/"delivery.jsonl")
             if e.get("status")=="CONFIRMED" and e.get("incident_id")}
    if incident_id in already:
        return "ALREADY_CONFIRMED"
    # Use gh preconfigured auth; do not embed token, server address or raw logs.
    message=f"P2 #126 watchdog INCIDENT {incident_id}: testing signal missing. See protected Vela evidence. No success verdict; investigate."
    run=subprocess.run(["gh","issue","comment","126","-R","tevriqorg/P2-AIBOX",
                        "--body",message],capture_output=True,text=True,timeout=15,check=False)
    if run.returncode != 0:
        append_event(state_dir/"delivery.jsonl",{"incident_id":incident_id,"status":"FAILED",
                     "at_utc":now_utc(),"reason":"GitHub comment transport failed"})
        return "NOT_DELIVERED"
    append_event(state_dir/"delivery.jsonl",{"incident_id":incident_id,"status":"CONFIRMED","at_utc":now_utc()})
    return "CONFIRMED"


def main():
    p=argparse.ArgumentParser();p.add_argument("--state-dir",type=Path,required=True)
    p.add_argument("--notify",action="store_true",
                   help="Explicitly post sanitized incident to GitHub if authenticated")
    args=p.parse_args()
    status=check(args.state_dir)
    if status["result"]=="FAIL" and args.notify:
        status["delivery"]=notify_issue_once(args.state_dir,status["incident_id"])
    print(json.dumps(status,sort_keys=True))
    sys.exit(0 if status["result"]=="PASS" else 2)


if __name__=="__main__":
    main()
