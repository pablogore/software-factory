# factoryd: la fábrica como servidor en Go (propuesta)

Fecha: 2026-11-05. Decisiones de Pablo: la fábrica pasa a ser un servidor en Go; la orquestación deja de vivir en prompts; **el servidor es agnóstico del agente** (Claude Code, Codex, Gemini CLI, Copilot CLI, OpenCode, Pi…).

## Principio
- **Go decide todo lo determinista**: estado, transiciones, cola, concurrencia, worktrees, puertos, gates, permisos, GitHub, Discord, costo.
- **El LLM solo hace lo que requiere juicio**: escribir la spec, construir, revisar, aprobar. Sigue habiendo prompts, pero solo de rol (qué buscar), cortos, sin protocolo, rutas ni estado, y sin nada específico de un agente.
- **Nada del núcleo depende de una capacidad que tenga un solo agente** (hooks de Claude Code, `--json-schema`, subagentes, skills). Lo específico vive en adaptadores.

## Tres capas, tres protocolos
| Dirección | Protocolo | Por qué |
|---|---|---|
| Orquestador → agente (lanzar, prompt, cancelar, reanudar, permisos) | **ACP** (Agent Client Protocol, JSON-RPC por stdio) | Estándar abierto de Zed; lo hablan Gemini CLI (nativo), Claude Code (adaptador `@agentclientprotocol/claude-agent-acp`), Codex (`@zed-industries/codex-acp`), Copilot CLI, OpenCode y otros. Hay SDK de Go. |
| Agente → orquestador (contexto, progreso, preguntas, resultado) | **MCP** servido por factoryd (`modelcontextprotocol/go-sdk`, streamable HTTP en localhost), con la misma API también por CLI (`factory worker …`) para agentes sin MCP como Pi | ACP permite pasar los servidores MCP al crear la sesión. Token por sesión → el servidor sabe qué job y qué rol llama. |
| Seguridad | **Del sistema, no del agente** | Los hooks no son portables. |

### Interfaz en Go
```go
type Agent interface {
    Start(ctx context.Context, s SessionSpec) (Session, error) // rol, worktree, modelo, MCP, prompt de rol
}
type Session interface {
    Prompt(ctx context.Context, msg string) (<-chan Event, error) // progreso, tool calls, uso
    Cancel() error
    Resume(ctx context.Context) error // si el agente soporta cargar sesión; si no, sesión nueva + traspaso
    Close() error
}
```
- Adaptador **ACP** genérico = el camino principal.
- Adaptadores **nativos** opcionales (ej. `claude -p` stream-json, `codex exec --json`) solo si hace falta algo que ACP no da en un agente (p. ej. costo exacto o un modelo puntual).
- Config: rol → agente y modelo; se pueden mezclar agentes por rol (ej. construye uno y revisa otro). Ver `04-roles.md`.

### Resultado final
`--json-schema` es solo de Claude → el resultado se entrega con **`submit_result`** (tool MCP o `factory worker submit-result`), validado por el servidor contra el schema del rol. Si el agente termina sin llamarlo o con JSON inválido: un reintento con el error; si falla de nuevo, el intento cuenta como fallido.

### Herramientas del trabajador (pocas, por rol)
| Tool | Quién | Para qué |
|---|---|---|
| `submit_result` | todos | resultado estructurado del rol (VERDICT, STATUS, decisión…) |
| `context_pack` | todos | spec, diffstat, archivos, decisiones, learnings |
| `progress` | todos | avance para la observabilidad y los canales |
| `ask_human` | spec-writer, approver | pregunta estructurada → needs-info / needs-human |
| `dev_server` | builder, ux, ui | puerto + levantar server (el servidor es dueño del proceso) |
| `run_gate` | builder | checks deterministas |
| `record_finding` | revisores | hallazgo con archivo:línea (memoria entre rondas) |

### Seguridad agnóstica
1. **Sin credenciales en el agente**: no tiene token de GitHub ni de push. Commit/push/PR/comentarios los hace factoryd (outbox). El agente solo deja cambios en su worktree.
2. **Aislamiento por sesión**: usuario de sistema o contenedor por job con solo su worktree montado (los revisores, de solo lectura). Red limitada al proveedor del modelo + registries.
3. **Permisos ACP** (`session/request_permission`): el servidor contesta según el rol (best effort, depende de cada agente).
4. **Verificación después**: el servidor revisa el diff (archivos permitidos por rol, tests borrados/`.skip`, paths protegidos) y re-corre los checks sin agente antes del merge.
- Los hooks de Claude Code pueden seguir como capa extra en su adaptador, pero no son la garantía.

### Lo que se pierde por ser agnóstico (mínimo común)
- Subagentes, skills y hooks propios de cada agente: no se usan desde el núcleo.
- Reanudar sesión no lo soportan todos → traspaso por `build.md`/`context_pack` como camino normal.
- Costo/tokens no viene uniforme: tomarlo del agente si lo informa; si no, estimar o leer del proveedor.
- Calidad distinta por agente → métricas por (rol, agente, modelo) para decidir el ruteo con datos.

## Componentes
| Componente | Hace | Reemplaza |
|---|---|---|
| Store (SQLite, sin cgo) | jobs, etapas, intentos, eventos (append-only), costos | job.json + flocks (y el deadlock de `queue`) |
| Máquina de estados | intake → spec → specgate → build → gate → review → approve → merge / needs-human / needs-info; iniciativas y slices | lógica repartida entre SKILL.md y `factory/bin/job` |
| Ingreso de eventos | webhooks + polling de respaldo, desde cualquier proveedor (`03-integraciones.md`) | `job queue` por polling con `gh` |
| Outbox | toda escritura externa (incluidos push y PR) se guarda antes y se reintenta con idempotencia | llamadas `gh` sueltas |
| Scheduler | slots, slot reservado para humanos, presupuesto por job, guard de rate limit | `concurrency` en config |
| Workspaces | worktrees, puertos, dev servers en process group, aislamiento por job, `doctor` | scripts + limpieza manual |
| Agentes | interfaz `Agent` + adaptador ACP + nativos opcionales | Agent tool + SendMessage |
| MCP / CLI trabajador | canal agente → orquestador | escribir archivos en factory/jobs |
| Gates | specgate, tests/lint/build, tests debilitados, diff permitido por rol, re-correr checks sin agente | `gate`, `specgate` |
| API + SSE | operación desde el agente del humano y observabilidad de solo lectura (`06-interfaz-y-observabilidad.md`) | dashboard con polling |
| Canales | Discord, Telegram… como proveedores (`03-integraciones.md`) | plugin de Channels + sesión de Claude prendida |

## Migración por fases
1. **F0 contrato**: schemas de `submit_result` por rol; eventos y estados; interfaz `Agent`.
2. **F1 núcleo**: store + máquina de estados + adaptador ACP con Claude Code (el agente que ya usamos) + `submit_result` y `context_pack`. Paridad con `factory/bin/job`; portar los tests. Correr en sombra.
3. **F1b segundo agente**: Codex, Gemini o Pi en un rol (ej. reviewer-code) para probar que la interfaz no tiene nada de Claude.
4. **F2 GitHub**: webhooks + outbox (push y PR desde el servidor) + feedback del PR que reanuda el job.
5. **F3 operación**: aislamiento por job, resto de las tools, canales, presupuestos, `doctor`, métricas por agente, observabilidad.
6. **F4 corte**: se borra el orquestador de SKILL.md.

## Riesgos
- ACP es joven y la cobertura varía por agente (permisos, cargar sesión, uso de tokens): tests de contrato por adaptador y versiones fijadas.
- Los adaptadores de Claude y Codex a ACP son de terceros (Zed / comunidad), no del CLI mismo.
- Webhooks necesitan URL pública (túnel) o polling.
- Sigue atado a una máquina (worktrees locales).
- Condiciones de uso de cada suscripción para uso headless continuo; en servidor 24/7 puede convenir API key.
- Cada tool suma contexto a cada agente: pocas y por rol.
