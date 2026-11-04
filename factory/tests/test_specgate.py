"""Tests for factory/bin/specgate. Run: python3 -m unittest discover factory/tests"""

import subprocess
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
GATE = HERE.parent / "bin" / "specgate"
VALID = (HERE / "fixtures" / "spec-valid.md").read_text()


def run(kind, text, tmp, *flags):
    f = Path(tmp) / "x.md"
    f.write_text(text)
    res = subprocess.run([str(GATE), kind, str(f), *flags], capture_output=True, text=True)
    return res.returncode, res.stdout


class SpecGateTest(unittest.TestCase):
    def setUp(self):
        import tempfile
        self.tmp = tempfile.mkdtemp()

    def check(self, text, want_ok, needle="", kind="spec", flags=()):
        code, out = run(kind, text, self.tmp, *flags)
        self.assertEqual(code == 0, want_ok, out)
        self.assertIn(needle, out)

    def test_valid_spec_passes(self):
        self.check(VALID, True, "SPECGATE: PASS")

    def test_sixty_line_spec_fails(self):
        self.check((HERE / "fixtures" / "spec-60-lines.md").read_text(), False, "60 lines")

    def test_bad_status(self):
        self.check(VALID.replace("STATUS: READY", "STATUS: NEEDS-INFO"), False, "first line")
        self.check(VALID.replace("STATUS: READY", "# STATUS: READY"), False, "first line")

    def test_criteria_count(self):
        self.check(VALID.replace("3. Un usuario no ve pedidos de otro usuario.\n", ""), False, "2 numbered")
        extra = "".join(f"{i}. x\n" for i in (4, 5, 6))
        self.check(VALID.replace("\n## Fuera", extra + "\n## Fuera"), False, "6 numbered")

    def test_valor_and_demo_required_and_non_empty(self):
        self.check(VALID.replace("## Demo", "## Otra"), False, "missing section `## Demo`")
        self.check(VALID.replace("Quien compra ve sus pedidos en una sola pantalla.\n", ""), False, "`## Valor` is empty")

    SLICES = "| ID | Tipo | Valor | Estado |\n|----|------|-------|--------|\n| S1 | slice | a | {} |\n| S2 | spike | b | {} |\n"

    def test_slices_exactly_one_next(self):
        self.check(self.SLICES.format("next", "idea"), True, kind="slices")
        self.check(self.SLICES.format("next", "next"), False, "found 2", kind="slices")
        self.check(self.SLICES.format("idea", "idea"), False, "found 0", kind="slices")

    def test_slices_done_allows_no_next_but_not_a_next(self):
        self.check(self.SLICES.format("merged", "done"), True, kind="slices", flags=["--done"])
        self.check(self.SLICES.format("merged", "next"), False, "DONE but", kind="slices", flags=["--done"])

    def test_slices_rejects_status_line_and_bad_state(self):
        self.check("STATUS: CONTINUE\n" + self.SLICES.format("next", "idea"), False, "must not have a STATUS", kind="slices")
        self.check(self.SLICES.format("next", "wip"), False, "state 'wip'", kind="slices")


if __name__ == "__main__":
    unittest.main()
