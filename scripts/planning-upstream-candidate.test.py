"""Small git fixtures verify that sync stops before an unsafe merge."""
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("planning-upstream-candidate.sh").resolve()


class CandidateTest(unittest.TestCase):
    def test_clean_merge_preserves_custom_work(self):
        self.exercise("clean", 0)

    def test_number_collision_stops_before_merge(self):
        self.exercise("collision", 3)

    def test_content_conflict_stops_with_unmerged_candidate(self):
        self.exercise("conflict", 1)

    def exercise(self, kind, expected):
        with tempfile.TemporaryDirectory(prefix="planning-sync-test-") as directory:
            root = Path(directory)

            def git(*args):
                return subprocess.check_output(["git", *args], cwd=root, text=True, stderr=subprocess.DEVNULL).strip()

            def write(path, value):
                target = root / path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(value)
                git("add", path)

            git("init", "-b", "main")
            git("config", "user.name", "Git test fixture")
            git("config", "user.email", "fixture@example.test")
            write("README", "Base\n")
            git("commit", "-m", "base")
            git("switch", "-c", "upstream-fixture")
            if kind == "collision":
                write("server/migrations/565_upstream.up.sql", "SELECT 1;\n")
            elif kind == "conflict":
                write("README", "Upstream\n")
            else:
                write("upstream.txt", "Upstream\n")
            git("commit", "-m", "upstream")
            git("switch", "main")
            write("server/migrations/565_planning.up.sql", "SELECT 2;\n")
            if kind == "conflict":
                write("README", "Custom\n")
            git("commit", "-m", "custom")
            before = git("rev-parse", "HEAD")
            result = subprocess.run(["bash", str(SCRIPT), "upstream-fixture"], cwd=root, capture_output=True, text=True)
            self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
            self.assertTrue((root / "server/migrations/565_planning.up.sql").exists())
            if expected == 0:
                self.assertTrue((root / "upstream.txt").exists())
                self.assertEqual(len(git("show", "-s", "--format=%P").split()), 2)
            else:
                self.assertEqual(git("rev-parse", "HEAD"), before)
                if kind == "conflict":
                    self.assertTrue(git("ls-files", "--unmerged"))
                else:
                    self.assertEqual(git("status", "--porcelain"), "")


if __name__ == "__main__":
    unittest.main()
