"""#126: a scoped read key must never escape the dedicated canary endpoint."""
import http.server
import json
import tempfile
from unittest.mock import patch
from pathlib import Path
import sys
import threading
import unittest
from urllib.error import HTTPError

ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/"deploy/aibox-0.8/vela-uat/soak"))
import business_probe


class CanaryProbeSecurity(unittest.TestCase):
    def test_default_endpoint_is_dedicated_canary_not_old_uat(self):
        self.assertEqual(business_probe.API_URL,"http://127.0.0.1:18082/api/v1")

    def test_explicit_preflight_never_promotes_even_if_gate_installed(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)
            profile=root/"probe.json"
            profile.write_text(json.dumps({"kb_id":"00000000-0000-4000-8000-000000000001",
                "knowledge":[{"knowledge_id":"00000000-0000-4000-8000-000000000002",
                              "expected_phrase":"public oracle"}]}))
            profile.chmod(0o600)
            token=root/"key";token.write_text("scoped-test-token-long");token.chmod(0o600)
            gate={"trial_id":"P2-126-example","oracle_sha256":"f"*64}
            def mock(url,auth):
                if "/chunks/" in url:
                    return {"success":True,"data":[{"content":"public oracle"}]}
                return {"success":True,"data":{"id":"00000000-0000-4000-8000-000000000002",
                    "knowledge_base_id":"00000000-0000-4000-8000-000000000001"}}
            with patch.object(business_probe,"validate_gate",return_value=(gate,"approved")):
                outcome=business_probe.run(root,profile,token,transport=mock,preflight=True)
            self.assertEqual(outcome["result"],"PRECHECK_PASS")
            self.assertIsNone(outcome["trial_id"])

    def test_http_redirect_never_forwards_scoped_read_key(self):
        received=[]
        class Target(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                received.append(self.headers.get("X-API-Key"))
                self.send_response(200);self.end_headers()
                self.wfile.write(b'{"success":true,"data":{}}')
            def log_message(self,*args):pass
        dest=http.server.ThreadingHTTPServer(("127.0.0.1",0),Target)
        class Redirect(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(302)
                self.send_header("Location",f"http://127.0.0.1:{dest.server_port}/leak")
                self.end_headers()
            def log_message(self,*args):pass
        src=http.server.ThreadingHTTPServer(("127.0.0.1",0),Redirect)
        for server in (src,dest):
            t=threading.Thread(target=server.serve_forever,daemon=True);t.start()
        original=business_probe.API_URL
        try:
            business_probe.API_URL=f"http://127.0.0.1:{src.server_port}/api/v1"
            with self.assertRaises(HTTPError):
                business_probe.api_get("/knowledge/abc","dummy-scoped-key")
            self.assertEqual(received,[],"redirect leaked scoped read credential")
        finally:
            business_probe.API_URL=original
            for server in (src,dest):
                server.shutdown();server.server_close()


if __name__=="__main__":unittest.main()
