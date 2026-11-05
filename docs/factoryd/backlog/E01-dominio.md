EPIC: Núcleo de dominio y máquina de estados pura

## Why
Hoy la lógica del flujo está repartida entre un prompt (SKILL.md) y un script de Python con estado en archivos, y se rompe en casos borde (iniciativas, rework, rondas). factoryd necesita un núcleo que decida cada transición de forma determinista y testeable sin red, sin agentes y sin base de datos.

## Outcome
- Todo el flujo de un job (de ticket a merge, con rondas, escalados, feedback e iniciativas) se recorre en tests del paquete `decide`, sin IO.
- Los comportamientos que cubren los tests de la fábrica actual (`factory/tests` en `main`) tienen un caso equivalente que pasa.

## Out of scope
Persistencia, agentes, GitHub y canales: llegan en épicas siguientes.

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/01-diseno.md`, `02-diagramas.md` (vida de un job), `04-roles.md`, `07-arquitectura.md`. Reglas de paquetes en `AGENTS.md`.

---
STORY: Modelo de dominio de jobs, roles y resultados
DEPENDS: none

As a desarrollador de factoryd, I want los tipos del dominio definidos y validados en un solo lugar, so that el resto del sistema hable el mismo idioma.

## Acceptance criteria
1. Given el paquete `internal/domain`, when se lo compila, then existen los tipos Job, Stage, Pipeline, Role, Kind (intake, spec, build, review, approve, task), Result por kind, Finding, Origin, Subscription y Event, sin importar nada fuera de la biblioteca estándar.
2. Given un Result de review con un hallazgo sin archivo o sin severidad, when se lo valida, then devuelve un error que nombra el campo faltante.
3. Given un Job, when se lo serializa a JSON y se lo vuelve a leer, then queda igual (test de ida y vuelta).
4. Given el repo, when se agregan los primeros tests, then usan go-specs fijado a `v0.4.0` (`go get github.com/getsyntegrity/go-specs@v0.4.0`) con las convenciones de `AGENTS.md`, y `make check` pasa.

## Notes
Los Result por kind son los schemas de `submit_result` de `01-diseno.md`. Un Finding tiene archivo, línea, severidad, categoría y rol (formato común de `04-roles.md`).

---
STORY: Pipeline declarativo en YAML con validación
DEPENDS: 1

As a operador de la fábrica, I want describir el flujo (etapas, roles en paralelo, rondas) en un archivo YAML, so that pueda cambiar el flujo sin tocar código.

## Acceptance criteria
1. Given el `pipelines/default.yaml` de `04-roles.md`, when se lo carga, then se obtiene un Pipeline con las etapas intake, spec, build, review (5 roles en paralelo, vuelve a build, máximo 3 rondas) y approve.
2. Given un pipeline sin etapa build, o con dos, when se lo valida, then el error lo dice.
3. Given una etapa review sin `on_changes`, o una referencia a un rol que no existe, when se lo valida, then el error nombra la etapa y el rol.
4. Given un rol con `when` (paths, labels, deps_changed, diff_lines) y `mode` (shadow, advisory, blocking), when se lo carga, then esos campos quedan disponibles y un modo desconocido es un error.

## Notes
El parseo de YAML puede usar una librería externa solo si vive fuera de `domain` (por ejemplo en un adaptador de config); `domain` recibe la estructura ya parseada y la valida.

---
STORY: Decide recorre el camino feliz de un job
DEPENDS: 1, 2

As a desarrollador de factoryd, I want una función pura que, dado un job, un evento y el pipeline, devuelva los eventos siguientes, so that todas las transiciones se decidan en un solo lugar testeable.

## Acceptance criteria
1. Given un job nuevo y el pipeline default, when se aplican en orden los resultados ACCEPT, spec READY, specgate OK, build READY, gate OK, cinco reviews PASS y APPROVE, then el job termina en merge pedido y cada paso emitió el evento esperado (test de tabla).
2. Given un job en spec, when llega un resultado de review, then Decide devuelve un error de transición ilegal y no cambia nada.
3. Given la misma entrada dos veces, when se llama a Decide, then devuelve exactamente lo mismo (sin reloj ni azar: el tiempo viene dentro del evento).

## Notes
Diagrama "Vida de un job" en `02-diagramas.md`. Firma orientativa en `07-arquitectura.md`.

---
STORY: Decide maneja rondas, cambios pedidos y escalados
DEPENDS: 3

As a operador de la fábrica, I want que el flujo vuelva a build cuando un revisor pide cambios y que escale a una persona cuando se agotan las rondas o el approver lo pide, so that ningún job quede dando vueltas para siempre.

## Acceptance criteria
1. Given una review con un CHANGES de un rol en modo blocking, when terminan todos los revisores de la ronda, then el job vuelve a build con la ronda incrementada.
2. Given una ronda ≥ 2, when se decide qué revisores correr, then solo vuelven los roles que pidieron CHANGES más los marcados `always_rerun`.
3. Given un CHANGES de un rol en modo shadow o advisory, when termina la ronda, then no bloquea: el job sigue a approve.
4. Given la última ronda permitida con CHANGES, o un gate que falla con el presupuesto agotado, o un ESCALATE del approver, when se decide, then el job pasa a needs-human con el motivo.
5. Given un spec rechazado por specgate tres veces, when se decide, then pasa a needs-human (paridad con la fábrica actual).

---
STORY: Decide maneja preguntas, feedback humano y reapertura
DEPENDS: 4

As a persona que pidió el trabajo, I want que mis respuestas y comentarios reanuden el job donde corresponde, so that no tenga que volver a pedir todo.

## Acceptance criteria
1. Given un job en needs-info, when llega la respuesta de una persona permitida, then vuelve a la etapa que hizo la pregunta (no siempre a intake).
2. Given un job en needs-human, when llega feedback de rework, then vuelve a build con el feedback como entrada; when llega una aprobación, then pasa a merge.
3. Given un job terminado, when una persona permitida comenta en uno de sus hilos, then el job se reabre en build con ese comentario; when el comentario viene de un bot o CI, then no se reabre.
4. Given un job que una persona detuvo, when llega cualquier mensaje, then no se reabre.

## Notes
Suscripciones multi-hilo: `03-integraciones.md` (Job y origen). Semántica tomada de dsh-superagent (ver `docs/factoryd/03-integraciones.md`).

---
STORY: Decide maneja iniciativas en slices y paridad con los tests actuales
DEPENDS: 5

As a operador de la fábrica, I want que un issue grande se trabaje como iniciativa en slices con replan después de cada merge, so that factoryd haga todo lo que hace la fábrica actual.

## Acceptance criteria
1. Given una iniciativa con tres slices, when se mergea el primero y el replan dice CONTINUE con checkpoint every-slice, then la iniciativa espera la aprobación humana antes del siguiente slice; con checkpoint auto, then arranca el siguiente.
2. Given un replan DONE, when se decide, then la iniciativa se cierra; given NEEDS-HUMAN, then pasa a needs-human.
3. Given un comentario `[direction]` sobre un slice sin mergear, when se decide, then el slice queda descartado y el job rechazado (no "merged").
4. Given los casos de `factory/tests/test_job.py`, `test_initiative_fixes.py` y `test_spec_exception.py` de `main`, when se los traduce a tablas de casos de `decide`, then todos pasan; los que no aplican quedan listados con el motivo en el PR.

## Notes
Leer los tests con `git show origin/main:factory/tests/<archivo>`. Los arreglos recientes de iniciativas (Refs en vez de Closes, direction sobre PR sin mergear, checkpoint sin slot, needs-info vuelve a `info_from`) están cubiertos por esos tests.
