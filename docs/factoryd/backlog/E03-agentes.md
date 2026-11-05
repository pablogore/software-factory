EPIC: Agentes intercambiables por ACP y superficie del trabajador

## Why
La fábrica actual solo funciona con Claude Code porque depende de su herramienta de subagentes, sus hooks y sus formatos. factoryd tiene que poder usar Claude Code, Codex, Gemini, OpenCode o Pi en cualquier rol, y mezclar agentes (por ejemplo, que construya uno y revise otro).

## Outcome
- Un mismo job corre con el builder en Claude Code y un revisor en otro agente, sin código específico de ninguno en el núcleo.
- Cada agente entrega su resultado estructurado y el servidor lo valida.

## Out of scope
Sandbox y aislamiento (épica de workspaces). Interfaz del humano (épica de operación desde el agente).

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/01-diseno.md` (ACP, MCP, tools), `06-interfaz-y-observabilidad.md` (CLI del trabajador para agentes sin MCP), `04-roles.md`.

---
STORY: Lanzar agentes por ACP con un adaptador genérico
DEPENDS: E01.1

As a operador de la fábrica, I want que factoryd lance cualquier agente compatible con ACP con un prompt de rol en un directorio, so that no dependa de un agente en particular.

## Acceptance criteria
1. Given la configuración de un agente (comando y argumentos, por ejemplo `gemini --acp` o el adaptador ACP de Claude Code), when se inicia una sesión, then factoryd crea la sesión con el directorio de trabajo y los servidores MCP del job, y manda el prompt.
2. Given una sesión corriendo, when se cancela o vence su tiempo, then el proceso del agente termina y no queda huérfano.
3. Given un agente falso de prueba que habla ACP y responde con un guion, when se corre la suite de contrato del puerto `Agent`, then pasa (todos los tests del núcleo usan este falso).
4. Given los eventos de la sesión (mensajes, tool calls, uso de tokens si el agente lo informa), when llegan, then se publican como eventos de progreso del job.

## Notes
Interfaz `Agent`/`Session` en `01-diseno.md`. Hay SDK de ACP para Go; si no conviene, implementar JSON-RPC por stdio.

---
STORY: Entregar el resultado con submit_result, por MCP o por CLI
DEPENDS: 1

As a operador de la fábrica, I want que cada agente entregue su resultado en un formato validado, so that el flujo no dependa de que el modelo escriba "VERDICT" en la primera línea.

## Acceptance criteria
1. Given un agente con MCP, when llama a la tool `submit_result` con un JSON válido para el kind de su rol, then factoryd lo acepta y lo convierte en el evento de resultado del job.
2. Given un agente sin MCP (como Pi), when ejecuta `factory worker submit-result` con el mismo JSON y el token del job en una variable de entorno, then el efecto es idéntico.
3. Given un JSON inválido, o un agente que termina sin entregar resultado, when se detecta, then se le da un reintento con el error; si vuelve a fallar, el intento cuenta como fallido.
4. Given un token de job, when se intenta usar para una operación de otro job o del operador, then se rechaza.

---
STORY: Contexto, progreso y hallazgos para los agentes
DEPENDS: 2

As a agente que trabaja en un job, I want pedir el contexto del job, informar progreso y registrar hallazgos, so that no tenga que explorar el repo de cero ni escribir archivos sueltos.

## Acceptance criteria
1. Given un job, when un agente pide `context_pack`, then recibe la spec, el diff contra la base, la lista de archivos tocados, las decisiones y learnings relevantes, y el mismo contenido en el mismo orden para todos los revisores de la ronda (para aprovechar el caché del proveedor).
2. Given un agente, when llama a `progress`, then el mensaje aparece como evento del job.
3. Given un revisor, when llama a `record_finding` con archivo, línea, severidad y categoría, then el hallazgo queda guardado con su rol y en la ronda siguiente se le entrega para que lo vuelva a verificar.
4. Given cada tool, when la llama un rol que no la tiene permitida, then se rechaza.

---
STORY: Preguntar a una persona, correr el gate y levantar dev servers
DEPENDS: 3

As a agente que trabaja en un job, I want pedirle algo a una persona, correr los checks y levantar la app para probarla, so that pueda terminar mi trabajo sin salir de las reglas de la fábrica.

## Acceptance criteria
1. Given un spec-writer o approver, when llama a `ask_human` con una pregunta y opciones, then el job pasa a needs-info (o needs-human) y la pregunta queda lista para los canales.
2. Given un builder, when llama a `run_gate`, then recibe el resultado de los checks deterministas del job sin poder cambiarlos.
3. Given un builder o un revisor de UX/UI, when pide `dev_server`, then factoryd asigna un puerto libre del rango del job, levanta el comando configurado y lo mata al terminar la etapa.
4. Given un agente sin MCP, when usa los equivalentes `factory worker ask-human|run-gate|dev-server`, then el efecto es idéntico.

---
STORY: Prompts de rol y mapeo rol → agente y modelo
DEPENDS: 2, E01.2

As a operador de la fábrica, I want que cada rol tenga su prompt corto y elija agente y modelo por configuración, so that pueda cambiar quién hace cada cosa sin tocar código.

## Acceptance criteria
1. Given los agentes de `.claude/agents/` en `main` (intake, spec-writer, builder, cuatro revisores, approver, tech-lead, product-owner), when se migran, then existen como `roles/*.md` + `roles.yaml` sin instrucciones de protocolo, rutas ni estado (eso lo hace factoryd).
2. Given `roles.yaml` con `agent` y `model` por rol, when se lanza un rol, then se usa ese agente y modelo.
3. Given un rol revisor configurado con el mismo agente y modelo que el builder del job, when se valida la configuración, then aparece una advertencia (no un error).
4. Given un rol con modelos por nivel de riesgo (`model: {low: …, medium: …, high: …}`), when intake marca el riesgo del job, then se usa el modelo de ese nivel (paridad con `routing` de `factory/config.json` en `main`).
5. Given `language` en la configuración, when un agente escribe specs, reviews o mensajes para personas, then usa ese idioma.

## Notes
Formato de rol en `04-roles.md`.

---
STORY: Un job real con Claude Code y un segundo agente
DEPENDS: 4, 5, E05.5

As a operador de la fábrica, I want ver un job de punta a punta con Claude Code como builder y otro agente (Codex, Gemini, OpenCode o Pi) como revisor, so that quede probado que el núcleo no tiene nada de un agente en particular.

## Acceptance criteria
1. Given un repo de prueba y un issue chico, when se corre el job con el builder en Claude Code (vía ACP) y `reviewer-code` en otro agente, then el job llega a approve y el resultado de cada uno quedó validado.
2. Given el mismo job con Pi como revisor (sin MCP), when corre, then usa `factory worker` y el resultado es equivalente.
3. Given la prueba, when se documenta, then el PR incluye cómo configurar cada agente probado y qué capacidad faltó en cada uno (reanudar sesión, costo, permisos).

## Notes
Esta historia se prueba a mano o con un test marcado que requiere los agentes instalados; no corre en `make check`.
