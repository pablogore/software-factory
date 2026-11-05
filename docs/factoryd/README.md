# factoryd: nueva arquitectura

Rama `arch/factoryd-go`. La fábrica pasa de ser una skill de Claude Code con prompts que orquestan a ser **factoryd**, un servidor en Go agnóstico del agente: Go decide todo lo determinista y los agentes (Claude Code, Codex, Gemini, OpenCode, Pi…) solo hacen el trabajo que requiere juicio.

La fábrica actual (`factory/`, `.claude/`) sigue funcionando en `main` mientras tanto.

## Documentos
Se leen en orden; cada uno usa conceptos de los anteriores.

| # | Documento | De qué trata |
|---|---|---|
| 1 | [01-diseno.md](01-diseno.md) | Principios, ACP + MCP, seguridad agnóstica, componentes, fases |
| 2 | [02-diagramas.md](02-diagramas.md) | Arquitectura, vida de un job, una etapa por dentro |
| 3 | [03-integraciones.md](03-integraciones.md) | Abstract factory de proveedores: GitHub, GitLab, Jira, Discord, Telegram; tickets desde el chat |
| 4 | [04-roles.md](04-roles.md) | Roles y pipeline dinámicos en YAML; modos shadow, advisory y blocking |
| 5 | [05-config.md](05-config.md) | Configuración como código: export, import y sync con GitHub |
| 6 | [06-interfaz-y-observabilidad.md](06-interfaz-y-observabilidad.md) | Se opera desde el agente (CLI + skill, MCP opcional); TUI y desktop de solo lectura con kanban |
| 7 | [07-arquitectura.md](07-arquitectura.md) | Hexagonal + vertical slices en Go: estructura, reglas de dependencia, tests |
|  | [backlog/](backlog/README.md) | Épicas e historias (issues #12–#83) y cómo poner a trabajar a la fábrica actual |

Si dos documentos se contradicen, vale el de número más alto.

## Primer paso
`internal/domain` + `decide` (la máquina de estados pura) con los tests de `factory/tests` portados como tablas de casos. Ver el orden de construcción en `07-arquitectura.md`.
