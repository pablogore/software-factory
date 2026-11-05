EPIC: Operar la fábrica desde el agente que ya usás

## Why
No queremos otra app donde operar (como el dashboard de AO): quien usa Claude Code, OpenCode, Codex o Pi tiene que poder preguntar qué le espera, pedir trabajo y aprobar desde esa misma conversación, con las acciones sensibles confirmadas por una persona.

## Outcome
- Desde Claude Code y desde Pi se puede ver lo pendiente, crear un ticket y aprobar un job, sin abrir otra herramienta.
- Una aprobación nunca ocurre sin una confirmación humana explícita, en ningún canal.

## Out of scope
La observabilidad de solo lectura (épica de observabilidad).

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/06-interfaz-y-observabilidad.md`, `07-arquitectura.md` (registro de operaciones).

---
STORY: Registro de operaciones y API HTTP
DEPENDS: E05.1

As a desarrollador de factoryd, I want que cada slice declare sus operaciones una vez y que la API las exponga solas, so that todos los canales hagan exactamente lo mismo.

## Acceptance criteria
1. Given una operación registrada con nombre, schema de entrada y salida, scope y si es sensible, when arranca factoryd, then aparece en `POST /ops/<nombre>` y en un listado `GET /ops` con sus schemas.
2. Given un token de lectura, de operador o de job, when llama a una operación fuera de su scope, then recibe 403.
3. Given `GET /events` con SSE, when cambia un job, then el cliente recibe el evento.
4. Given la API, when escucha, then por defecto solo en localhost y con token.

---
STORY: CLI factory para operar
DEPENDS: 1

As a persona (o su agente), I want una CLI que hable con factoryd y devuelva JSON estable, so that cualquier agente pueda usarla.

## Acceptance criteria
1. Given factoryd corriendo, when se ejecuta `factory inbox|jobs|job <id>|ticket "<texto>"|approve <id>|feedback <id> "<texto>"|retry <id>|cancel <id>|watch|stats`, then cada comando llama a la operación correspondiente.
2. Given `--json`, when se ejecuta cualquier comando, then la salida es JSON con un schema documentado y estable; sin `--json`, una salida legible.
3. Given factoryd caído, when se ejecuta un comando, then el error lo dice en una línea y el código de salida no es 0.

---
STORY: Confirmación humana de acciones sensibles
DEPENDS: 2

As a dueño del repo, I want que aprobar, mergear, aplicar configuración o agregar roles exija una confirmación humana, so that un modelo no apruebe nada por interpretar mal una frase.

## Acceptance criteria
1. Given una operación sensible sin confirmación, when se llama, then factoryd responde con un código de un solo uso que vence en minutos y no ejecuta nada.
2. Given ese código, when la persona lo reenvía (por la CLI o tocando un botón en un chat), then se ejecuta; un código usado o vencido se rechaza.
3. Given un token de job, when intenta una operación sensible, then se rechaza siempre.
4. Given cada confirmación, when ocurre, then queda en el historial del job quién confirmó y desde qué canal.

---
STORY: Skill factory para Claude Code, OpenCode, Codex y Pi
DEPENDS: 3

As a persona que usa un agente de código, I want una skill que le enseñe a mi agente a usar la fábrica y mostrar sus datos bien, so that pueda decir "¿qué me necesita?" y ver una respuesta útil.

## Acceptance criteria
1. Given la skill `factory` (formato Agent Skills, `SKILL.md`), when se instala en Claude Code y en Pi, then el agente responde "¿qué me necesita?" con la lista de pendientes en una tabla y la acción sugerida para cada uno.
2. Given "mostrame el job N", when el agente responde, then muestra la línea de tiempo, el estado por rol y la spec con su diagrama Mermaid si lo tiene.
3. Given "aprobá el job N", when el agente lo intenta, then pasa por la confirmación humana (permiso nativo del agente o código de un solo uso).
4. Given Claude Code, when se instala, then las operaciones sensibles no quedan en la lista de permitidos; given Pi, then se incluye una extensión chica que muestra un diálogo de confirmación.

---
STORY: MCP del operador
DEPENDS: 2

As a persona que usa un agente con MCP, I want las mismas operaciones como tools, resources y prompts, so that mi agente las use sin pasar por la CLI.

## Acceptance criteria
1. Given el servidor MCP del operador, when un agente lo lista, then ve una tool por operación con el mismo nombre y schema que la API.
2. Given los resources `factory://inbox` y `factory://job/<id>`, when se leen, then devuelven lo mismo que `factory inbox --json` y `factory job <id> --json`.
3. Given una tool sensible, when se llama, then aplica la misma confirmación humana que la API.

---
STORY: Status line y avisos dentro del agente
DEPENDS: 2

As a persona trabajando en su agente, I want ver de reojo cuántas cosas me esperan, so that no tenga que preguntar todo el tiempo.

## Acceptance criteria
1. Given la status line de Claude Code configurada con `factory status --line`, when hay pendientes, then muestra "N te necesitan" en una línea corta y no tarda más de 200 ms.
2. Given una extensión de Pi incluida en el repo, when hay pendientes, then se muestra un widget con el mismo dato.
3. Given que factoryd no responde, when se pide la línea, then devuelve vacío sin error visible.
