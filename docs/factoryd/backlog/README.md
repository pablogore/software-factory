# Backlog de factoryd

El backlog completo de la nueva arquitectura, en el formato del product-owner de la fábrica (`EPIC:` / `STORY:` / `DEPENDS:`), cargado en GitHub como 13 épicas (#12–#22, #110 y #117) y 77 historias (#23–#83, #103, #111–#116, #118–#123 y #128–#130), todas con la etiqueta `factoryd`. La fábrica sumó #97 (seguimiento de E01.2) por su cuenta.

| Archivo | Épica | Issue |
|---|---|---|
| `E01-dominio.md` | Núcleo de dominio y máquina de estados pura | #12 |
| `E02-persistencia.md` | Persistencia, eventos y ejecución confiable | #13 |
| `E03-agentes.md` | Agentes intercambiables por ACP y superficie del trabajador | #14 |
| `E04-workspaces.md` | Workspaces aislados, gates y seguridad | #15 |
| `E05-flujo.md` | Flujo completo de un job, de ticket a merge | #16 |
| `E06-integraciones.md` | Integraciones: GitHub primero, chats que inician trabajo | #17 |
| `E07-operacion.md` | Operar la fábrica desde el agente | #18 |
| `E08-config.md` | Configuración como código y roles dinámicos | #19 |
| `E09-observabilidad.md` | Observabilidad de solo lectura: kanban en TUI y desktop | #20 |
| `E10-cierre.md` | Extensiones, migración y corte de la fábrica vieja | #21 |
| `E11-memoria.md` | Memoria: decisiones y aprendizajes | #22 |
| `E12-paralelo.md` | Una historia en paralelo: arquitecto, builders e integración | #110 |
| `E13-calidad.md` | Confianza y operación: replays, evals, seguridad, dogfooding y releases | #117 |

- `issues.json`: id de cada historia (ej. `E05.3`) → número de issue.
- `plan.md`: orden de arranque (`ORDER`) y dependencias (`AFTER`) para la cola de la fábrica.
- `file_issues.py`: el script que cargó todo (`check` para validar los archivos).

Solo las historias de E01 (#23–#28 y #116) tienen la etiqueta `factory` (en cola). El resto está en `factory:draft`: se encolan cuando se planifica el sprint (product-owner) o agregando la etiqueta `factory` a mano.

## Cómo poner a trabajar a la fábrica actual sobre esta rama

La fábrica actual (en `main`) corre desde tu checkout de `main` y crea los worktrees de cada job desde `origin/<base_branch>`. En tu checkout de `main`:

1. **Rama base y idioma** en `factory/config.json`:
   ```json
   "base_branch": "arch/factoryd-go",
   "language": "es"
   ```
2. **Checks**: el gate corre el `factory/checks.sh` de `main` dentro del worktree del job. Para esta rama tiene que correr Go:
   ```bash
   #!/usr/bin/env bash
   set -euo pipefail
   exec make check
   ```
   Requiere Go 1.27+ en la máquina (lo pide go-specs; con `GOTOOLCHAIN=auto`, el default, Go lo baja solo); golangci-lint es opcional (si no está, se saltea).
3. **Paths protegidos** (recomendado) en `factory/config.json`, para que ningún job toque las reglas del proyecto:
   ```json
   "protected_paths": ["factory/checks.sh", "factory/config.json", "AGENTS.md", "Makefile",
                       "internal/arch_test.go", ".github/workflows/", "docs/factoryd/0"]
   ```
4. **Dependencias entre historias**: copiar `plan.md` a `factory/jobs/plan.md` (es estado local, no se versiona). Sin eso, la cola no sabe que #24 espera a #23 y puede arrancarlas juntas.
5. Arrancar: `/factory run`.

Cuando termine E01, encolar la épica siguiente (o pedirle al product-owner que planifique el sprint con el backlog).

## Issues de la fábrica actual
Los cambios para la fábrica que vive en `main` llevan la etiqueta `legacy-factory` y no `factory`, para que la fábrica no los construya en esta rama: #124 (intake lee la rama base) y #125 (slices independientes en paralelo).

## Roadmap vs. comprometido
La etiqueta `roadmap` marca ideas a futuro que no cuentan como backlog comprometido: las épicas E12 (#110) y E13 (#117) con sus historias, y #128–#130. El avance se mide sobre las historias `factoryd` sin `roadmap`.

Para medirlo:
```bash
gh issue list -l factoryd --state all --limit 200 --json number,state,labels \
  --jq '[.[] | select(([.labels[].name] | index("epic") or index("roadmap")) | not)] | group_by(.state) | map({(.[0].state): length}) | add'
```

Regla para los replans: una nota menor de un revisor queda como "conocida" en el replan o como criterio de una historia existente; se abre un issue nuevo solo si es un bug real o un agujero de seguridad.

## Estado (2026-11-07)
- Cerradas: E01 (#12) y E02 (#13). E03, E04 y E05 en curso.
- Agregadas durante la construcción: #175 (factoryd serve arma todas las piezas), #201 (endurecer pedidos y demo del worker), #205 (rodeos al gate), y en la fábrica actual (`legacy-factory`) #124, #125, #160 y #187.
- El avance comprometido se mide con el comando de la sección "Roadmap vs. comprometido".
