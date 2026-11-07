package spec_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/slices/spec"
)

// The cases of factory/tests/test_specgate.py on main, with its fixtures in
// testdata. A case passes when specgate exits 0 there, i.e. on PASS.

func fixture(name string) string {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		panic(err) // the fixtures are part of the package
	}
	return string(b)
}

var valid = fixture("spec-valid.md")

type gateRow struct {
	name   string
	text   string
	longOK bool
	ok     bool
	needle string
}

func checkRow(ctx *specs.Context, r gateRow) {
	rep := spec.Check(r.text, r.longOK)
	ctx.Expect(rep.Verdict == spec.VerdictPass).ToEqual(r.ok)
	ctx.Expect(rep.String()).To(specs.Contain(r.needle))
}

func repeat(word string, n int) string {
	return strings.TrimSuffix(strings.Repeat(word+" ", n), " ")
}

func numbered(from, to int, text string) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "%d. %s\n", i, text)
	}
	return b.String()
}

const diagram = "## Diagrama\n```mermaid\nflowchart LR\n%s```\n\n"

func flow(n int, node string) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "  "+node+" --> A%d\n", i, i+1)
	}
	return b.String()
}

func specgateRows() []gateRow {
	const crit = "## Criterios de aceptación\n"
	one := strings.Split(valid, crit)[0] + crit + "1. x\n\n## Fuera" + strings.SplitN(valid, "\n## Fuera", 2)[1]
	const third = "3. Un usuario no ve pedidos de otro usuario.\n"
	filler := strings.Repeat("- "+repeat("z", 25)+"\n", 3)
	wordy := strings.NewReplacer(
		"- El total ya existe en cada pedido.\n", filler,
		"Quien compra ve sus pedidos en una sola pantalla.", repeat("v", 30),
		"## Flag\nninguno", "## Flag\n"+repeat("f", 60),
	).Replace(valid)
	four := ""
	for i := range 4 {
		four += fmt.Sprintf("- cosa %d\n", i)
	}
	optional := strings.SplitN(valid, "\n## Fuera", 2)[0] + "\n\n## Demo" +
		strings.SplitN(strings.SplitN(valid, "## Demo", 2)[1], "\n## Flag", 2)[0] + "\n"
	withDiagram := func(d string) string { return strings.Replace(valid, "## Criterios", d+"## Criterios", 1) }
	oneDiagram := fmt.Sprintf(diagram, "  A --> B\n")
	return []gateRow{
		{"the valid spec passes", valid, false, true, "SPECGATE: PASS"},
		{"a sixty-line spec fails", fixture("spec-60-lines.md"), false, false, "60 lines"},
		{"another status on the first line fails", strings.Replace(valid, "STATUS: READY", "STATUS: NEEDS-INFO", 1), false, false, "first line"},
		{"a status that isn't the first line fails", strings.Replace(valid, "STATUS: READY", "# STATUS: READY", 1), false, false, "first line"},
		{"one criterion passes", one, false, true, "SPECGATE: PASS"},
		{"no criteria fail", strings.Replace(one, "1. x\n", "", 1), false, false, "0 numbered"},
		{"five criteria fail", strings.Replace(valid, "\n## Fuera", numbered(4, 5, "x")+"\n## Fuera", 1), false, false, "5 numbered"},
		{"a 31-word criterion fails", strings.Replace(valid, third, "3. "+repeat("x", 31)+"\n", 1), false, false, "criterion 3 has 31 words"},
		{"a criterion wrapped over two lines still counts as one", strings.Replace(valid, third, "3. "+repeat("x", 20)+"\n   "+repeat("y", 11)+"\n", 1), false, false, "criterion 3 has 31 words"},
		{"too many words in total fail", wordy, false, false, "words; the maximum is 250"},
		{"four items out of scope fail", strings.Replace(valid, "- Filtros → S2\n", four, 1), false, false, "`## Fuera de alcance` has 4 items"},
		{"five demo steps fail", strings.Replace(valid, "1. Abrí", numbered(1, 5, "paso")+"Abrí", 1), false, false, "`## Demo` has"},
		{"Fuera de alcance, Supuestos and Flag are optional", optional, false, true, "SPECGATE: PASS"},
		{"a spec without Demo fails", strings.Replace(valid, "## Demo", "## Otra", 1), false, false, "missing section `## Demo`"},
		{"an empty Valor fails", strings.Replace(valid, "Quien compra ve sus pedidos en una sola pantalla.\n", "", 1), false, false, "`## Valor` is empty"},
		{"a 15-line diagram doesn't count toward the spec", withDiagram(fmt.Sprintf(diagram, flowWords(14))), false, true, "SPECGATE: PASS"},
		{"a 16-line diagram fails", withDiagram(fmt.Sprintf(diagram, flow(15, "A%d"))), false, false, "diagram has 16 lines"},
		{"two diagrams fail", withDiagram(oneDiagram + oneDiagram), false, false, "2 mermaid diagrams"},
	}
}

func flowWords(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "  A%d[Paso con varias palabras %d] --> A%d\n", i, i, i+1)
	}
	return b.String()
}

func TestSpecgate(t *testing.T) {
	specs.Describe(t, "specgate on a spec", func(s *specs.Spec) {
		specs.Table(s, specgateRows(), func(r gateRow) string { return r.name }, checkRow)

		s.It("lets a spec longer than 30 lines through when the extra lines are its diagram", func(ctx *specs.Context) {
			text := strings.Replace(valid, "## Criterios", fmt.Sprintf(diagram, flowWords(14))+"## Criterios", 1)
			ctx.Expect(len(strings.Split(strings.TrimRight(text, "\n"), "\n"))).To(specs.BeGreaterThan(30))
		})

		s.It("prints the bad first line quoted as main does", func(ctx *specs.Context) {
			rep := spec.Check(strings.Replace(valid, "STATUS: READY", "STATUS: NEEDS-INFO", 1), false)
			ctx.Expect(rep.Errors).ToEqual([]string{"first line must be `STATUS: READY` or `STATUS: SPLIT`, not 'STATUS: NEEDS-INFO'"})
			ctx.Expect(rep.String()).ToEqual("SPECGATE: FAIL\n- first line must be `STATUS: READY` or `STATUS: SPLIT`, not 'STATUS: NEEDS-INFO'\n")
		})
	})
}
