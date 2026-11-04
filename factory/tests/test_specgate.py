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
        crit = "## Criterios de aceptación\n"
        one = VALID.split(crit)[0] + crit + "1. x\n\n## Fuera" + VALID.split("\n## Fuera")[1]
        self.check(one, True, "SPECGATE: PASS")
        self.check(one.replace("1. x\n", ""), False, "0 numbered")
        extra = "".join(f"{i}. x\n" for i in (4, 5))
        self.check(VALID.replace("\n## Fuera", extra + "\n## Fuera"), False, "5 numbered")

    def test_long_criterion_fails(self):
        long = "3. " + " ".join(["x"] * 31) + "\n"
        self.check(VALID.replace("3. Un usuario no ve pedidos de otro usuario.\n", long), False, "criterion 3 has 31 words")
        # a criterion wrapped over two lines still counts as one
        wrapped = "3. " + " ".join(["x"] * 20) + "\n   " + " ".join(["y"] * 11) + "\n"
        self.check(VALID.replace("3. Un usuario no ve pedidos de otro usuario.\n", wrapped), False, "criterion 3 has 31 words")

    def test_total_words(self):
        filler = "".join("- " + " ".join(["z"] * 25) + "\n" for _ in range(3))
        text = VALID.replace("- El total ya existe en cada pedido.\n", filler).replace(
            "Quien compra ve sus pedidos en una sola pantalla.", " ".join(["v"] * 30))
        text = text.replace("## Flag\nninguno", "## Flag\n" + " ".join(["f"] * 60))
        self.check(text, False, "words; the maximum is 250")

    def test_short_lists(self):
        four = "".join(f"- cosa {i}\n" for i in range(4))
        self.check(VALID.replace("- Filtros → S2\n", four), False, "`## Fuera de alcance` has 4 items")
        steps = "".join(f"{i}. paso\n" for i in range(1, 6))
        self.check(VALID.replace("1. Abrí", steps + "9. Abrí").replace("9. Abrí", "Abrí"), False, "`## Demo` has")

    def test_optional_sections(self):
        text = VALID.split("\n## Fuera")[0] + "\n\n## Demo" + VALID.split("## Demo")[1].split("\n## Flag")[0] + "\n"
        self.check(text, True, "SPECGATE: PASS")

    def test_valor_and_demo_required_and_non_empty(self):
        self.check(VALID.replace("## Demo", "## Otra"), False, "missing section `## Demo`")
        self.check(VALID.replace("Quien compra ve sus pedidos en una sola pantalla.\n", ""), False, "`## Valor` is empty")

    DIAGRAM = "## Diagrama\n```mermaid\nflowchart LR\n{}```\n\n"

    def test_diagram_does_not_count_but_has_its_own_limit(self):
        ok = self.DIAGRAM.format("".join(f"  A{i}[Paso con varias palabras {i}] --> A{i+1}\n" for i in range(14)))
        text = VALID.replace("## Criterios", ok + "## Criterios")
        self.assertGreater(len(text.splitlines()), 30)
        self.check(text, True, "SPECGATE: PASS")
        big = self.DIAGRAM.format("".join(f"  A{i} --> A{i+1}\n" for i in range(15)))
        self.check(VALID.replace("## Criterios", big + "## Criterios"), False, "diagram has 16 lines")
        one = self.DIAGRAM.format("  A --> B\n")
        self.check(VALID.replace("## Criterios", one + one + "## Criterios"), False, "2 mermaid diagrams")

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
