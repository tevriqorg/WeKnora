import hashlib
import json
from pathlib import Path
import sys
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT/"deploy/aibox-0.8/vela-uat/soak"))
from corpus_verify import inspect_corpus,SOURCE_FILENAME


class RealCorpusContract(unittest.TestCase):
    def test_real_file_sha_is_independent_and_failure_is_detected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp); data=root/"corpus";data.mkdir()
            items=[]
            for source_id,name in SOURCE_FILENAME.items():
                b=("source:"+source_id).encode()
                (data/name).write_bytes(b)
                items.append({"source_id":source_id,"sha256":hashlib.sha256(b).hexdigest(),"size_bytes":len(b)})
            manifest=root/"oracle.json";manifest.write_text(json.dumps({"items":items}))
            self.assertEqual(inspect_corpus(data,manifest)["source_count"],len(items))
            (data/"rfc9111.pdf").write_bytes(b"tampered" + (data/"rfc9111.pdf").read_bytes())
            with self.assertRaises(ValueError):
                inspect_corpus(data,manifest)


if __name__=="__main__":unittest.main()
