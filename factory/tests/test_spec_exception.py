"""A spec over the normal length can ask for an exception; a human decides.

- specgate: EXCEPTION (exit 3) within the exception limits, PASS with --long-ok,
  FAIL past the exception limits or without a reason
- job spec-check: the exception goes to needs-human without spending a retry
- job spec-ok: back to spec, spec-check passes, the slice goes on to rate
- job feedback instead: back to spec, the spec-writer must shorten it

Run: python3 -m unittest discover factory/tests
"""

import subprocess
import tempfile
import unittest
from pathlib import Path

from test_e2e import E2EBase
from test_initiative_fixes import InitiativeFixesE2E

GATE = Path(__file__).resolve().parent.parent / "bin" / "specgate"


def long_spec(criteria=5, words=40, reason="El mapeo y el bloqueo no sirven por separado: partirlo deja un slice sin Demo."):
    crit = "".join(f"{i}. " + " ".join(["palabra"] * words) + "\n" for i in range(1, criteria + 1))
    exc = f"\n## Excepción de largo\n{reason}\n" if reason is not None else ""
    return (f"STATUS: READY\n\n# S1: hacer algo\n\n## Valor\nQuien compra puede hacer algo.\n\n"
            f"## Criterios de aceptación\n{crit}\n## Demo\n1. Abrir la pantalla y verlo.\n{exc}")


def gate(text, *flags):
    with tempfile.NamedTemporaryFile("w", suffix=".md", delete=False) as f:
        f.write(text)
    res = subprocess.run([str(GATE), "spec", f.name, *flags], capture_output=True, text=True)
    return res.returncode, res.stdout


class SpecGateExceptionTest(unittest.TestCase):
    def test_without_reason_it_fails(self):
        code, out = gate(long_spec(reason=None))
        self.assertEqual(code, 1, out)
        self.assertIn("criterion 1 has 40 words", out)

    def test_with_reason_it_asks(self):
        code, out = gate(long_spec())
        self.assertEqual(code, 3, out)
        self.assertIn("SPECGATE: EXCEPTION", out)
        self.assertIn("> El mapeo y el bloqueo", out)
        self.assertIn("- 5 numbered criteria; need 1 to 4", out)

    def test_long_ok_passes(self):
        self.assertEqual(gate(long_spec(), "--long-ok")[0], 0)

    def test_past_the_exception_limits_fails(self):
        code, out = gate(long_spec(criteria=6), "--long-ok")
        self.assertEqual(code, 1, out)
        self.assertIn("(with the exception)", out)
        code, out = gate(long_spec(words=46))
        self.assertEqual(code, 1, out)

    def test_short_spec_with_reason_just_passes(self):
        self.assertEqual(gate(long_spec(criteria=2, words=10))[0], 0)

    def test_reason_must_be_short(self):
        code, out = gate(long_spec(reason=" ".join(["porque"] * 41)))
        self.assertEqual(code, 1, out)
        self.assertIn("`## Excepción de largo` has 41 words", out)


class SpecGateReviewFixesTest(unittest.TestCase):
    def test_short_spec_with_long_reason_passes(self):
        code, out = gate(long_spec(criteria=2, words=20, reason=" ".join(["razon"] * 50)))
        self.assertEqual(code, 0, out)

    def test_exception_text_does_not_count_toward_lines(self):
        reason = "\n".join(["motivo"] * 12)
        code, out = gate(long_spec(criteria=2, words=20, reason=reason))
        self.assertEqual(code, 0, out)

    def test_flags_are_per_subcommand(self):
        for kind, flag in (("spec", "--done"), ("slices", "--long-ok")):
            with tempfile.NamedTemporaryFile("w", suffix=".md", delete=False) as f:
                f.write("x")
            res = subprocess.run([str(GATE), kind, f.name, flag], capture_output=True, text=True)
            self.assertEqual(res.returncode, 2, (kind, flag))

    def test_text_after_a_blank_line_is_not_part_of_the_criterion(self):
        spec = long_spec(criteria=1, words=25).replace("## Demo", "Nota suelta: " + "x " * 20 + "\n\n## Demo")
        spec = spec.replace("\n\nNota", "\n\nNota")
        code, out = gate(spec)
        self.assertEqual(code, 0, out)


class SpecExceptionE2E(E2EBase):
    # the helpers only, not InitiativeFixesE2E's tests
    rows, slices, init_dir, start = (InitiativeFixesE2E.rows, InitiativeFixesE2E.slices,
                                     InitiativeFixesE2E.init_dir, InitiativeFixesE2E.start)

    def test_exception_waits_for_a_human_then_goes_on(self):
        n, jid = self.start("Long spec", ("S1", "slice", "Uno", "next"))
        self.jobfile(jid, "spec.md", long_spec())
        out = self.job("spec-check", jid, ok=False).stdout
        self.assertIn("SPECGATE: EXCEPTION", out)
        j = self.stage(jid)
        self.assertEqual((j["stage"], j.get("spec_failures", 0)), ("needs-human", 0))
        self.assertIn("pide una excepción", self.gh()["issues"][n]["comments"][-1]["body"])
        # can't skip the question by advancing to rate
        self.assertIn("not a legal transition", self.job("advance", jid, "rate", ok=False).stderr)
        self.job("spec-ok", jid)
        self.assertEqual(self.stage(jid)["stage"], "spec")
        self.assertIn("SPECGATE: PASS", self.job("spec-check", jid).stdout)
        self.job("advance", jid, "rate")
        self.assertEqual(self.stage(jid)["stage"], "rate")

    def test_feedback_sends_it_back_to_be_shortened(self):
        n, jid = self.start("Long spec 2", ("S1", "slice", "Uno", "next"))
        self.jobfile(jid, "spec.md", long_spec())
        self.job("spec-check", jid, ok=False)
        out = self.job("feedback", jid, input="Acortala, sacá el criterio 5\n").stdout
        self.assertIn("→ spec", out)
        self.assertNotIn("spec_exception", self.stage(jid))
        # still long and no OK: it asks again
        self.jobfile(jid, "spec.md", long_spec())
        self.assertIn("SPECGATE: EXCEPTION", self.job("spec-check", jid, ok=False).stdout)
        self.job("reject", jid, input="cleanup")


if __name__ == "__main__":
    unittest.main()
