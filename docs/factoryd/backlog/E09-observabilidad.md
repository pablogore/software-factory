EPIC: Observabilidad de solo lectura: kanban en TUI y desktop

## Why
Hoy el dashboard muestra el estado leyendo archivos con polling. Queremos ver la fábrica en vivo, en la terminal o en una app de escritorio, sin que esas pantallas sean otro lugar donde decidir: se mira ahí, se opera desde el agente o el chat.

## Outcome
- Un kanban en vivo con columnas que salen del pipeline muestra en qué está cada job y cómo va cada revisor.
- Se puede saber cuánto cuesta cada rol, agente y modelo, y si la fábrica está sana.

## Out of scope
Acciones desde la TUI o la desktop (a propósito: son de solo lectura).

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/06-interfaz-y-observabilidad.md` (kanban, vistas, builds).

---
STORY: API de lectura para tablero, inbox y línea de tiempo
DEPENDS: E07.1

As a desarrollador de las vistas, I want consultas de lectura con un token propio, so that la TUI y la desktop no necesiten permisos de escritura.

## Acceptance criteria
1. Given un token de lectura, when se consultan `board`, `inbox`, `job/<id>/timeline`, `sessions`, `stats` y `health`, then responden; when intenta una operación de escritura, then 403.
2. Given `board`, when se consulta, then las columnas salen del pipeline vigente (con "Te necesita" primera) y cada tarjeta trae id, título, origen, agente y modelo activos, ronda, tiempo en la columna, costo y el estado por rol en review.
3. Given un cliente suscripto por SSE, when cambia un job, then recibe solo el delta de esa tarjeta.
4. Given el cliente Go de la API, when se compila, then vive en `pkg/` y no importa nada de `internal/`.

---
STORY: factory top: kanban en la terminal
DEPENDS: 1

As a persona en la terminal o por SSH, I want ver el kanban en vivo, so that sepa en qué está la fábrica sin abrir nada más.

## Acceptance criteria
1. Given `factory top`, when se abre, then muestra el kanban con las columnas del pipeline y se actualiza solo.
2. Given una tarjeta seleccionada, when se presiona enter, then se abre su línea de tiempo; la pantalla muestra el comando para actuar (ej. `factory approve 42`) pero no ejecuta nada.
3. Given carriles por proyecto o por iniciativa, when se alternan con una tecla, then el tablero se reagrupa.
4. Given una terminal de 80 columnas, when se muestra, then sigue siendo legible (columnas colapsables).

## Notes
Bubble Tea, Bubbles y Lip Gloss.

---
STORY: TUI: en vivo, línea de tiempo y log de agentes
DEPENDS: 2

As a operador de la fábrica, I want ver qué está haciendo cada agente ahora, so that detecte rápido uno trabado o caro.

## Acceptance criteria
1. Given la vista En vivo, when hay sesiones corriendo, then se ven job, rol, agente, modelo, tiempo, costo y slots ocupados, además de dev servers y puertos.
2. Given la línea de tiempo de un job, when se abre, then muestra eventos, intentos, duración y costo por etapa, la spec en texto, el diff coloreado y los hallazgos por rol.
3. Given una sesión seleccionada, when se abre su log, then se ve el stream de progreso y tool calls en vivo.

---
STORY: Desktop embebida en el binario
DEPENDS: 1

As a persona que prefiere una ventana, I want una app de escritorio con el mismo kanban y vistas, so that tenga la fábrica a la vista mientras trabajo.

## Acceptance criteria
1. Given `factory-desktop`, when se abre, then muestra el kanban y las vistas de la TUI, con la spec renderizando su diagrama Mermaid.
2. Given que se conecta a un factoryd local o remoto (URL + token de lectura), when cambia algo, then se actualiza en vivo.
3. Given el build, when se compila, then usa Wails con el frontend embebido; el servidor y la CLI siguen compilando sin cgo.
4. Given un job que pasa a "Te necesita", when ocurre, then aparece una notificación nativa.

---
STORY: Costos, métricas y salud
DEPENDS: 1

As a operador de la fábrica, I want ver costos, rendimiento por rol y salud del sistema, so that decida el ruteo de modelos con datos y detecte problemas.

## Acceptance criteria
1. Given `stats`, when se consulta, then muestra costo por job, rol, agente y modelo; aprobación en ronda 1; tasa de rework humano; y comparación shadow vs blocking por rol.
2. Given `health`, when se consulta, then muestra colas, rate limit restante de cada proveedor, errores del outbox, drift de configuración y restos detectados por doctor.
3. Given la TUI y la desktop, when se abren estas vistas, then muestran los mismos datos.

---
STORY: Exportar trazas y métricas a OpenTelemetry
DEPENDS: 5

As a equipo con Grafana u otro backend, I want recibir trazas y métricas estándar, so that integre la fábrica a la observabilidad que ya tengo.

## Acceptance criteria
1. Given un endpoint OTLP configurado, when corre un job, then se exporta una traza por job con un span por etapa y uno por sesión de agente, con tokens y costo como atributos.
2. Given sin endpoint configurado, when corre factoryd, then no hay costo ni dependencia de red extra.
