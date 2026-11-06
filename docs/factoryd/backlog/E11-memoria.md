EPIC: Memoria de la fábrica: decisiones y aprendizajes

## Why
Cada agente arranca de cero y vuelve a descubrir lo mismo (cómo levantar el server, decisiones de diseño ya tomadas), y a veces contradice decisiones anteriores. Guardar todo automáticamente se vuelve ruido; la memoria útil es poca, aprobada por una persona y leída por índice.

## Outcome
- Los agentes respetan las decisiones vigentes del repo y proponen reemplazarlas en vez de ignorarlas.
- Baja la cantidad de re-descubrimientos por job (medido en 10 jobs antes y después).

## Out of scope
Memorias externas (Engram, Mem0 y similares): solo si después de medir queda un hueco.

## Contexto
Rama base `arch/factoryd-go`. Se apoya en `context_pack` (`docs/factoryd/01-diseno.md`).

---
STORY: Registro de decisiones aprobado por personas
DEPENDS: E05.5, E03.3

As a dueño del repo, I want que las decisiones de diseño queden escritas en el repo y que los agentes las lean, so that no se contradigan de un job a otro.

## Acceptance criteria
1. Given una iniciativa que termina, when corre el replan, then puede proponer decisiones nuevas como archivos en `docs/decisions/` dentro del PR; nunca se guardan sin que una persona mergee ese PR.
2. Given `docs/decisions/`, when cambia, then el gate verifica que `docs/decisions/INDEX.md` esté regenerado y coincida.
3. Given un job, when un agente pide `context_pack`, then recibe el índice y solo el texto de las decisiones relevantes para los archivos y el tema del job.
4. Given un cambio que contradice una decisión vigente, when lo detecta un revisor, then lo marca como hallazgo y propone reemplazar la decisión en vez de ignorarla.

---
STORY: Learnings operativos verificados y medición
DEPENDS: 1

As a operador de la fábrica, I want una lista corta de hechos operativos verificados (cómo levantar el server, comandos que fallan), so that los agentes no gasten tokens redescubriéndolos.

## Acceptance criteria
1. Given `.factory/learnings.md`, when el approver cierra un job y encontró un hecho operativo nuevo y verificado, then lo propone con fecha y origen en el PR; el archivo nunca supera 100 líneas y se poda lo viejo.
2. Given `context_pack`, when se arma, then incluye los learnings.
3. Given 10 jobs antes y 10 después, when se pide `factory stats --rediscovery`, then se comparan tokens por agente y comandos de exploración repetidos.

---
STORY: Aprender de las correcciones humanas
DEPENDS: 2, E05.6

As a persona que corrige a la fábrica, I want que una corrección que doy una vez se proponga como regla para que no tenga que repetirla, so that la fábrica aprenda de mí y no solo de lo que el approver deduce.

## Acceptance criteria
1. Given un feedback de rework desde needs-human, when el job termina, then la fábrica propone como mucho una regla corta derivada de esa corrección y pregunta en el canal de origen si guardarla; sin respuesta, no guarda nada.
2. Given que la persona confirma, when se guarda, then va como cambio al repo de configuración (por PR, como cualquier cambio de configuración) en la capa que elija: org, team, proyecto o rol.
3. Given reglas en capas, when se arma `context_pack`, then se incluyen todas las que aplican al repo y al rol, sin que una pise a otra; dos reglas que se contradicen se detectan al confirmar la nueva.
4. Given un job sin correcciones humanas, when termina, then no se pregunta nada.

## Notes
Idea tomada del learning loop de AI-DLC: una corrección confirmada en el gate queda como regla en capas org → team → project → phase.
