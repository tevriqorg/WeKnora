"""Second independent review P1 red cases, hermetic; no user data."""
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/"scripts"))
sys.path.insert(0,str(ROOT/"deploy/aibox-0.8/vela-uat/soak"))
from p2_canary_guard import verify_canary_runtime
from soak_engine import append_event,evaluate
from test_soak_runtime import SoakRuntimeContract


def fake_runtime(root):
    state={"clone_db":"aibox126_canary_test","original_db":"weknora"}
    env={"DB_NAME":state["clone_db"],"REDIS_DB":"14",
         "REDIS_ADDR":"127.0.0.1:56379","REDIS_PREFIX":"aibox126-canary",
         "WEKNORA_REDIS_NAMESPACE":"aibox126-canary",
         "LOCAL_STORAGE_BASE_DIR":str(root/"storage"),
         "AIBOX_INGRESS_ALLOWED_RESOURCE_PREFIX":"local://10000/aibox126/"}
    procs={"api":dict(env,WEKNORA_RUNTIME_ROLE="api",SERVER_HOST="127.0.0.1",SERVER_PORT="18082"),
           "worker":dict(env,WEKNORA_RUNTIME_ROLE="worker",
                         WEKNORA_WORKER_POOLS="core",SERVER_HOST="127.0.0.1",SERVER_PORT="18083")}
    return state,env,procs


class SecondReviewRed(unittest.TestCase):
    def test_private_storage_and_expected_ports_can_pass(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);(root/"storage").mkdir()
            state,env,procs=fake_runtime(root)
            self.assertTrue(verify_canary_runtime(root,state,env,procs))

    def test_shared_storage_symlink_is_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)/"canary";root.mkdir()
            shared=Path(d)/"shared";shared.mkdir()
            (root/"storage").symlink_to(shared,target_is_directory=True)
            state,env,procs=fake_runtime(root)
            self.assertRaises(ValueError,verify_canary_runtime,root,state,env,procs)

    def test_wrong_effective_server_port_is_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);(root/"storage").mkdir()
            state,env,procs=fake_runtime(root)
            procs["api"]["SERVER_PORT"]="18080"
            self.assertRaises(ValueError,verify_canary_runtime,root,state,env,procs)

    def test_binary_digest_matches_independently_pinned_bytes(self):
        from p2_canary_guard import verify_binary_digest
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/"server";p.write_bytes(b"exact-binary")
            digest=hashlib.sha256(p.read_bytes()).hexdigest()
            self.assertTrue(verify_binary_digest(p,digest))
            p.write_bytes(b"tampered")
            self.assertRaises(ValueError,verify_binary_digest,p,digest)

    def test_listener_socket_must_belong_to_expected_pid(self):
        from p2_canary_guard import listener_owned_by_pid
        with socket.socket() as sock:
            sock.bind(("127.0.0.1",0));sock.listen(1)
            port=sock.getsockname()[1]
            self.assertTrue(listener_owned_by_pid(os.getpid(),port))
            self.assertFalse(listener_owned_by_pid(99999999,port))

    def test_valid_looking_case_digest_without_file_cannot_count(self):
        obj=SoakRuntimeContract("test_signed_gate_enforces_integrity")
        obj.setUp();self.addCleanup(obj.doCleanups);obj.install_gate()
        append_event(obj.root/"case_results.jsonl",{
            "trial_id":obj.trial,"case_id":"AR01","result":"PASS","layer":"live",
            "source_id":"public-rfc","source_sha256":"1"*64,
            "p2_sha":obj.gate["p2_sha"],"weknora_sha":obj.gate["weknora_sha"],
            "runtime_digest":obj.gate["runtime_digest"],
            "oracle_hash":obj.gate["oracle_sha256"],
            "oracle_sha256":obj.gate["oracle_sha256"],
            "timestamp_utc":(obj.start+dt.timedelta(hours=2)).isoformat(),
            "evidence_sha256":"2"*64})
        verdict=evaluate(obj.root,now=obj.start+dt.timedelta(hours=360),gate_key=obj.key)
        self.assertIn("AR01",verdict["missing_case_ids"])

    def test_real_content_addressed_case_evidence_and_frozen_source(self):
        obj=SoakRuntimeContract("test_signed_gate_enforces_integrity")
        obj.setUp();self.addCleanup(obj.doCleanups)
        source="1"*64
        oracle={"schema":"p2-126-frozen-oracle-v1",
                "sources":[{"source_id":"public-rfc","sha256":source}]}
        frozen=json.dumps(oracle,sort_keys=True,separators=(',',':')).encode()
        (obj.root/"frozen-oracle.json").write_bytes(frozen)
        (obj.root/"frozen-oracle.json").chmod(0o600)
        obj.gate["oracle_sha256"]=hashlib.sha256(frozen).hexdigest()
        obj.install_gate()
        payload={
            "trial_id":obj.trial,"case_id":"AR01","result":"PASS","layer":"live",
            "source_id":"public-rfc","source_sha256":source,
            "p2_sha":obj.gate["p2_sha"],"weknora_sha":obj.gate["weknora_sha"],
            "runtime_digest":obj.gate["runtime_digest"],
            "oracle_hash":obj.gate["oracle_sha256"],
            "oracle_sha256":obj.gate["oracle_sha256"],
            "timestamp_utc":(obj.start+dt.timedelta(hours=2)).isoformat(),
            "assertions":[{"name":"external raw bit identical","passed":True}]}
        raw=json.dumps(payload,sort_keys=True,separators=(',',':')).encode()
        digest=hashlib.sha256(raw).hexdigest()
        directory=obj.root/"case-evidence";directory.mkdir(mode=0o700)
        artifact=directory/(digest+".json");artifact.write_bytes(raw);artifact.chmod(0o600)
        event=dict(payload,evidence_sha256=digest)
        append_event(obj.root/"case_results.jsonl",event)
        verdict=evaluate(obj.root,now=obj.start+dt.timedelta(hours=360),gate_key=obj.key)
        self.assertNotIn("AR01",verdict["missing_case_ids"])
        # A forged source digest with a correspondingly re-hashed artifact is
        # still rejected by the separately signed frozen source manifest.
        payload["source_sha256"]="2"*64
        tampered=json.dumps(payload,sort_keys=True,separators=(',',':')).encode()
        digest2=hashlib.sha256(tampered).hexdigest()
        (directory/(digest2+".json")).write_bytes(tampered)
        (directory/(digest2+".json")).chmod(0o600)
        append_event(obj.root/"case_results.jsonl",dict(payload,evidence_sha256=digest2))
        self.assertNotEqual(digest,digest2)
        self.assertNotIn("AR01",evaluate(obj.root,now=obj.start+dt.timedelta(hours=360),
                                          gate_key=obj.key)["missing_case_ids"])
        # Original verified record remains valid; a bad extra record cannot replace it.
        artifact.unlink()
        self.assertIn("AR01",evaluate(obj.root,now=obj.start+dt.timedelta(hours=360),
                                       gate_key=obj.key)["missing_case_ids"])

    def test_open_p1_before_t0_then_closed_inside_window_invalidates(self):
        obj=SoakRuntimeContract("test_signed_gate_enforces_integrity")
        obj.setUp();self.addCleanup(obj.doCleanups);obj.install_gate()
        incident="P2-126-before-gate-critical"
        append_event(obj.root/"incidents.jsonl",{
            "incident_id":incident,"event_seq":1,"state":"DETECTED","severity":"P1",
            "case_id":"SC01","detected_utc":(obj.start-dt.timedelta(hours=2)).isoformat()})
        append_event(obj.root/"incidents.jsonl",{
            "incident_id":incident,"event_seq":2,"state":"VERIFIED_CLOSED","severity":"P1",
            "case_id":"SC01","timestamp_utc":(obj.start+dt.timedelta(hours=100)).isoformat()})
        verdict=evaluate(obj.root,now=obj.start+dt.timedelta(hours=360),gate_key=obj.key)
        self.assertIn(incident,verdict["trial_invalidated_by"])


if __name__=="__main__":unittest.main()
