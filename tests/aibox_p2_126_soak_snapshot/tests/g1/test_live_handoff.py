import tempfile
import unittest
from pathlib import Path
from implementation.ingress.live_handoff import HandoffError, WeKnoraHandoff


class LiveHandoffContract(unittest.TestCase):
    def setUp(self):
        self.calls=[]
        self.client=WeKnoraHandoff(api_url='http://127.0.0.1:18080/api/v1',bearer='test',kb_id='kb',transport=self.remote)

    def remote(self, endpoint, payload):
        self.calls.append((endpoint, payload))
        if endpoint == 'external-resource':
            return {'success':True,'data':{'resource_ref':'resource://raw-1','ownership':'external'}}
        return {'success':True,'data':{'id':'knowledge-1'}}

    def test_real_api_contract_never_mark_complete_locally(self):
        ref=self.client.register_external(resource_path='local://10000/aibox-uat-soak/raw.md',size=12,sha256='a'*64,filename='raw.md',mime='text/markdown')
        rev='sha256:'+'a'*64
        receipt=self.client.upsert_derived({'source_key':'test:raw','source_revision_id':rev,'idempotency_key':'sha256:stable','title':'Raw','processor':'test','processor_version':'1','markdown':'text'},resource_ref=ref)
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'receipt.json'
            self.client.save_receipt(path,receipt)
            assert '"verified_by_weknora": false' in path.read_text()
        self.assertEqual([x[0] for x in self.calls],['external-resource','derived'])

    def test_rejects_nonhex_source_integrity_claim(self):
        with self.assertRaises(HandoffError):
            self.client.register_external(resource_path="local://10000/aibox-uat-soak/raw.md",size=12,sha256="z"*64,filename="raw.md",mime="text/markdown")

    def test_rejects_bad_ref_and_receipt(self):
        with self.assertRaises(HandoffError):
            self.client.upsert_derived({'source_key':'a','source_revision_id':'rev','idempotency_key':'key'},resource_ref='local://fake')
        def bad(endpoint, payload):return {'success':True,'data':{'ownership':'external'}}
        self.client._transport=bad
        with self.assertRaises(HandoffError):
            self.client.register_external(resource_path='local://10000/aibox-uat-soak/raw.md',size=1,sha256='b'*64,filename='raw.md',mime='text/markdown')



class ScopedAPIKeyWireContract(unittest.TestCase):
    def test_scoped_key_uses_native_header_and_not_jwt(self):
        import json
        import threading
        from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
        seen = []
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args): pass
            def do_POST(self):
                seen.append(dict(self.headers))
                good = self.headers.get('X-API-Key') == 'scoped-test-key' and not self.headers.get('Authorization')
                self.send_response(200 if good else 401)
                self.end_headers()
                self.wfile.write(json.dumps({'success':good,'data':{'resource_ref':'resource://test','ownership':'external'}}).encode())
            do_GET = do_POST
        server = ThreadingHTTPServer(('127.0.0.1',0),Handler)
        worker = threading.Thread(target=server.serve_forever,daemon=True)
        worker.start()
        try:
            client = WeKnoraHandoff(api_url=f'http://127.0.0.1:{server.server_port}/api/v1',api_key='scoped-test-key',kb_id='kb')
            self.assertEqual('resource://test',client.register_external(resource_path='local://10000/test/raw.md',size=1,sha256='a'*64,filename='raw.md',mime='text/markdown'))
            import sys
            sys.path.insert(0,str(Path(__file__).resolve().parents[2]/'deploy/aibox-0.8/vela-uat/soak'))
            import business_probe
            original_url = business_probe.API_URL
            try:
                business_probe.API_URL = f'http://127.0.0.1:{server.server_port}/api/v1'
                self.assertTrue(business_probe.api_get('/knowledge/test','scoped-test-key')['success'])
            finally:
                business_probe.API_URL = original_url
            self.assertEqual(2,len(seen))
        finally:
            server.shutdown();server.server_close();worker.join(timeout=2)

    def test_ambiguous_or_missing_auth_is_rejected(self):
        for auth in ({},{'bearer':'jwt','api_key':'key'}):
            with self.assertRaises(ValueError):
                WeKnoraHandoff(api_url='http://127.0.0.1:18080/api/v1',kb_id='kb',**auth)

if __name__=='__main__':unittest.main()
