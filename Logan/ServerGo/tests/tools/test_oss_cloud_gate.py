import importlib.util
import os
import contextlib
import io
import json
from unittest import mock
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("cloud_gate", Path(__file__).resolve().parents[2] / "script/verify-cloud-e2e.py")
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class CloudGateTests(unittest.TestCase):
    def config(self):
        return {
            "LOGAN_IT_ALLOW_WRITES": "1", "LOGAN_IT_OSS_PRIVATE_BUCKET": "1",
            "LOGAN_IT_OSS_MODE": "aliyun", "LOGAN_IT_OSS_ENDPOINT": "https://oss-cn-hangzhou.aliyuncs.com",
            "LOGAN_IT_OSS_REGION": "cn-hangzhou", "LOGAN_IT_OSS_BUCKET": "isolated-test",
            "LOGAN_IT_OSS_PREFIX": "logan-it/acceptance/", "LOGAN_IT_OSS_ACCESS_KEY_ID": "synthetic-id",
            "LOGAN_IT_OSS_ACCESS_KEY_SECRET": "synthetic-secret",
        }

    def test_real_cloud_preconditions(self):
        settings = self.config()
        self.assertIsNone(gate.preflight(settings))
        for key, value in (("LOGAN_IT_OSS_PRIVATE_BUCKET", "0"), ("LOGAN_IT_OSS_MODE", "compatible"),
                           ("LOGAN_IT_OSS_ENDPOINT", "https://oss-cn-hangzhou.aliyuncs.com.attacker.test"),
                           ("LOGAN_IT_OSS_ENDPOINT", "https://oss-cn-hangzhou.aliyuncs.com/private"),
                           ("LOGAN_IT_OSS_ENDPOINT", "http://oss-cn-hangzhou.aliyuncs.com"),
                           ("LOGAN_IT_OSS_PREFIX", "production/"), ("LOGAN_IT_OSS_PREFIX", "logan-it/../")):
            with self.subTest(key=key, value=value):
                self.assertIsNotNone(gate.preflight(dict(settings, **{key: value})))
        self.assertIn("LOGAN_IT_OSS_ACCESS_KEY_SECRET", gate.preflight({}))

    def test_private_file_and_literal_values(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "cloud.env"
            path.write_text("LOGAN_IT_OSS_ACCESS_KEY_SECRET=$(do-not-execute)\n")
            path.chmod(0o600)
            self.assertEqual(gate.load_private_config(path)["LOGAN_IT_OSS_ACCESS_KEY_SECRET"], "$(do-not-execute)")
            path.chmod(0o644)
            with self.assertRaises(ValueError):
                gate.load_private_config(path)
            path.chmod(0o600)
            link = Path(directory) / "link"
            link.symlink_to(path)
            with self.assertRaises(OSError):
                gate.load_private_config(link)
            for content in ("AWS_SECRET_ACCESS_KEY=secret\n", "LOGAN_IT_OSS_MODE=aliyun\nLOGAN_IT_OSS_MODE=compatible\n", "export LOGAN_IT_ALLOW_WRITES=1\n"):
                path.write_text(content)
                with self.assertRaises(ValueError):
                    gate.load_private_config(path)

    def test_no_config_ignores_ambient_credentials_and_records_blocked(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "evidence"
            with mock.patch.dict(os.environ, self.config()), mock.patch("sys.argv", ["verify-cloud-e2e.py", "--out", str(output)]), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(gate.main(), 2)
            summary = json.loads((output / "summary.json").read_text())
            self.assertEqual(summary["status"], "blocked")
            self.assertTrue(all(item["status"] == "blocked" for item in summary["results"]))
            self.assertEqual((output / "summary.json").stat().st_mode & 0o777, 0o600)
            self.assertNotIn("synthetic-secret", (output / "summary.json").read_text())

    def test_generated_url_and_credentials_are_redacted(self):
        settings = self.config()
        diagnostic = 'GET "https://bucket.example/key?x-oss-signature=secret&x-oss-security-token=token" failed synthetic-secret'
        clean = gate.redacted(diagnostic, settings)
        for value in ("https://", "x-oss-signature", "security-token", "synthetic-secret"):
            self.assertNotIn(value, clean)


if __name__ == "__main__":
    unittest.main()
