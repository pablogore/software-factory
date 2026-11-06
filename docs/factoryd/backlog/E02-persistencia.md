EPIC: Persistencia, eventos y ejecución confiable

## Why
La fábrica actual guarda el estado en `job.json` con locks de archivo que pueden trabarse entre sí, y si la sesión se cae a mitad de un job no hay forma confiable de retomarlo. factoryd tiene que sobrevivir a reinicios sin perder ni duplicar trabajo.

## Outcome
- Matar factoryd en cualquier punto y volver a arrancarlo retoma los jobs sin repetir acciones externas.
- Ninguna escritura a GitHub o a un chat se hace dos veces por un reintento.

## Out of scope
Los adaptadores concretos de GitHub y canales (épica de integraciones).

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/01-diseno.md` (componentes), `07-arquitectura.md` (bus, EventLog, outbox).

---
STORY: Guardar jobs y eventos en SQLite de forma atómica
DEPENDS: E01.3

As a operador de la fábrica, I want que el estado de cada job y su historial de eventos se guarden juntos, so that nunca quede un estado sin el evento que lo explica.

## Acceptance criteria
1. Given un job y los eventos que devolvió Decide, when se aplican, then el nuevo estado y los eventos se guardan en una sola transacción; si algo falla, no se guarda ninguno.
2. Given dos procesos que aplican cambios al mismo job a la vez, when ambos escriben, then uno gana y el otro recibe un error de conflicto de versión (sin locks de archivo).
3. Given la base, when se lista el historial de un job, then aparecen todos sus eventos en orden con fecha.
4. Given el binario, when se compila con `CGO_ENABLED=0`, then funciona (driver SQLite en Go puro).

## Notes
El puerto `JobStore`/`EventLog` vive en `internal/ports`; el adaptador en `internal/adapters/out/sqlite`. Incluir un fake en memoria que cumpla la misma suite de contrato.

---
STORY: Bus de eventos que se reprocesa al arrancar
DEPENDS: 1

As a operador de la fábrica, I want que los slices reaccionen a eventos y que lo pendiente se procese al reiniciar, so that un corte no deje jobs colgados.

## Acceptance criteria
1. Given un evento guardado y no despachado, when factoryd arranca, then el bus lo despacha antes de aceptar trabajo nuevo.
2. Given un suscriptor que falla, when se despacha un evento, then se reintenta con espera creciente y el error queda registrado; los demás suscriptores no se ven afectados.
3. Given un evento ya procesado por un suscriptor, when se reprocesa por un reinicio, then el suscriptor no lo procesa dos veces.

---
STORY: Outbox para todo efecto externo
DEPENDS: 1

As a persona que mira el PR o el chat, I want que la fábrica no publique comentarios ni haga push dos veces, so that no se llene de ruido ni se rompan ramas.

## Acceptance criteria
1. Given una acción externa (comentar, push, abrir PR, mensaje a chat), when un slice la pide, then se guarda en el outbox en la misma transacción que el cambio de estado, con una clave de idempotencia.
2. Given una acción que falla por red o por un 5xx, when se reintenta, then usa espera creciente y el número de intentos queda visible.
3. Given una acción cuyo resultado se perdió (timeout), when se la reintenta, then primero se consulta si ya se hizo en el destino y no se repite si se hizo.
4. Given una acción que falla por permisos o validación, when ocurre, then no se reintenta: queda como fallida y el job pasa a needs-human.

---
STORY: Scheduler con slots, slot reservado para personas y presupuesto
DEPENDS: 2

As a operador de la fábrica, I want limitar cuántos agentes corren a la vez y cuánto puede gastar un job, so that no se agote la suscripción ni el presupuesto.

## Acceptance criteria
1. Given `concurrency: 2` y tres jobs listos, when se programan, then corren dos y el tercero queda en cola.
2. Given todos los slots ocupados, when llega una respuesta de una persona a un job, then se atiende igual con el slot reservado (el trabajo nuevo nunca bloquea contestarle a alguien).
3. Given un job con presupuesto de USD 5 que ya gastó 5, when va a lanzar otro agente, then pasa a needs-human con el motivo "presupuesto agotado".
4. Given el límite de la API de GitHub por debajo del umbral configurado, when se va a lanzar una etapa que la usa, then espera al reset en vez de fallar.

---
STORY: Cola con orden, dependencias entre issues y triage
DEPENDS: 4, E01.4

As a operador de la fábrica, I want que los issues se tomen en orden, respetando los que dependen de otros, y que un job trabado tenga una segunda mirada antes de llegar a mí, so that la fábrica no empiece cosas que no puede terminar ni me escale lo que se arregla solo.

## Acceptance criteria
1. Given un issue con "Depends on #N" en el cuerpo, when #N no está mergeado o cerrado, then el issue queda en espera y la cola lo muestra con el motivo.
2. Given un orden explícito (prioridad del product owner u orden del tech-lead), when hay slots libres, then se toma el siguiente según ese orden.
3. Given un job que choca contra una pared detectada por el sistema (último gate fallido, rondas agotadas, BLOCKED, merge fallido), when ocurre, then corre primero el rol de triage, que devuelve RETRY (una vez, con instrucciones para el builder) o ESCALATE (con una pregunta cerrada).
4. Given la paridad con `factory/bin/job queue` y el triage de `SKILL.md` en `main`, when se comparan los casos, then el comportamiento es el mismo o la diferencia queda explicada en el PR.

---
STORY: Modo de corrida: frenar entre issues, resumen y topes
DEPENDS: 4, 5

As a operador de la fábrica, I want que la fábrica frene al terminar cada issue y me muestre un resumen con el siguiente recomendado, o que corra desatendida con topes, so that controle el gasto sin tener que pedirle a mano que pare.

## Acceptance criteria
1. Given el modo por defecto, when una iniciativa termina (DONE), un job pasa a needs-human o needs-info, o un issue se rechaza, then la fábrica no arranca trabajo nuevo; lo que ya está corriendo termina, y los slices de una misma iniciativa siguen según su checkpoint sin frenar.
2. Given ese freno, when ocurre, then se publica en el canal de origen y en `factory inbox` un resumen con: issue, PRs, estado final, slices y rondas, costo real en USD, duración, el siguiente issue recomendado con una línea de por qué (dependencias, riesgo, valor) y las opciones continuar, elegir otro, replan o parar.
3. Given `factory run --auto [--max-jobs N] [--max-usd N]`, when corre, then no frena entre issues; se detiene al llegar a N issues terminados o a USD N gastados en la corrida, y frena igual ante needs-human o needs-info.
4. Given un issue que pasa de 5 slices o de USD 8 acumulados (configurables), when ocurre, then ese issue frena con el motivo aunque esté en modo `--auto`; los demás siguen.

## Notes
Viene de la fábrica actual, donde se agregó a `SKILL.md` después de tener que pedir "cuando termines esta ronda, frená" a mano. El tope de rondas no alcanza: el bucle de E01.2b fue de slices agregados por el replan, no de rondas.
