EPIC: Una historia en paralelo: el arquitecto arma el contrato, los builders programan y el arquitecto integra

## Why
Hoy cada historia la construye un solo builder, slice tras slice, aunque sus partes vivan en paquetes distintos y podrían avanzar a la vez. Queremos que la fábrica diga "esta historia la encaramos con dos programadores y un integrador" y lo haga sola, sin perder calidad.

## Outcome
- Una historia con partes independientes se termina en menos tiempo de reloj, con un solo PR revisado como cualquier otro.
- La fábrica sabe con datos cuándo conviene paralelizar y cuándo no.

## Out of scope
Paralelizar en la fábrica actual (no tiene rama de integración ni integrador). Ejecución en varias máquinas.

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/08-paralelizar.md`.

---
STORY: El arquitecto decide si una historia se paraleliza
DEPENDS: E05.2

As a operador de la fábrica, I want que un agente arquitecto proponga, para cada historia aceptada, si conviene construirla en paralelo y con qué partes, so that sepa por qué la fábrica va a usar más de un programador antes de que gaste.

## Acceptance criteria
1. Given una historia aceptada por intake, when corre el arquitecto, then devuelve SERIAL o PARALLEL; con PARALLEL, una lista de partes (2 o más), los archivos o paquetes de cada una y una línea de por qué.
2. Given partes cuyos archivos se superponen, o una historia que el arquitecto marca como chica, when se valida el plan, then factoryd lo rechaza y la historia sigue en serie.
3. Given un plan PARALLEL, when el modo es el de por defecto, then espera tu OK en el inbox y en el canal de origen; con `auto`, arranca solo.

## Notes
Criterios para paralelizar en `08-paralelizar.md` (archivos distintos, contrato fijable antes, historia grande).

---
STORY: Decide reparte el trabajo en partes y espera a que terminen
DEPENDS: E01.6

As a desarrollador de factoryd, I want que la máquina de estados maneje un job con partes que corren a la vez y se juntan en una etapa de integración, so that el paralelismo sea determinista y testeable como el resto del flujo.

## Acceptance criteria
1. Given un job con plan PARALLEL aprobado, when termina la etapa de contrato, then Decide crea una parte por builder y el job pasa a esperar las partes.
2. Given partes en curso, when una termina READY con su gate en verde, then el job sigue esperando; when terminan todas, then entra en la etapa de integración.
3. Given la integración, when el arquitecto devuelve un rework para una parte, then solo esa parte vuelve a build; las demás conservan su estado.
4. Given una parte que agota sus rondas o se bloquea, when ocurre, then el job pasa a needs-human con el nombre de la parte; las otras no se pierden.

---
STORY: Rama de integración y gate por parte
DEPENDS: 2, E04.1, E04.3

As a dueño del repo, I want que cada parte trabaje sobre la rama de integración y solo toque sus archivos, so that las partes no se pisen y el contrato no cambie a escondidas.

## Acceptance criteria
1. Given un job PARALLEL, when arranca, then existe una rama de integración `factory/<job-id>` y cada parte trabaja en su worktree sobre `factory/<job-id>-<parte>`, creada desde el contrato.
2. Given el gate de una parte, when su diff toca archivos fuera de los de su parte, o cambia archivos del contrato, then falla con el motivo.
3. Given el gate de la etapa de contrato, when el diff agrega lógica más allá de tipos, firmas y tests esqueleto, then falla con el motivo.
4. Given el job terminado, when se limpia, then se borran la rama de integración y las de cada parte.

---
STORY: El arquitecto arma el contrato y después integra
DEPENDS: 1, 3, E05.3

As a operador de la fábrica, I want que el mismo agente que diseñó el contrato sea el que integra las partes, so that quien mejor sabe cómo tienen que encajar sea quien las junte.

## Acceptance criteria
1. Given un plan PARALLEL aprobado, when corre la etapa de contrato, then el arquitecto escribe tipos, puertos, interfaces y tests esqueleto, más un `contract.md` con lo que cada parte tiene que cumplir.
2. Given todas las partes READY, when corre la integración, then se reanuda la misma sesión del arquitecto; si el agente no soporta reanudar, arranca uno nuevo con `contract.md` y los resúmenes de cada parte como traspaso.
3. Given la integración, when el arquitecto mergea las partes, then solo escribe pegamento (wiring, imports, adaptaciones mínimas); una parte que no cumple el contrato vuelve a su builder como rework y no se reescribe.
4. Given la integración terminada, when corre el gate completo y pasa, then el job sigue a los revisores como cualquier otro.

---
STORY: Revisión del total, un solo merge y métricas de paralelo
DEPENDS: 4, E05.5, E02.6

As a dueño del repo, I want que lo hecho en paralelo se revise y se mergee como un solo cambio, y saber si convino, so that el paralelismo no baje la calidad y se use solo cuando ahorra.

## Acceptance criteria
1. Given un job PARALLEL integrado, when corren los revisores, then ven el diff total con el contrato marcado; el arquitecto no participa de la revisión.
2. Given APPROVE, when se mergea, then entra a la base un solo PR con todas las partes.
3. Given jobs PARALLEL terminados, when se pide `factory stats --parallel`, then se compara tiempo de reloj, costo y rondas contra jobs en serie de tamaño parecido.
4. Given los topes de #103, when corre una historia en paralelo, then cuentan el trabajo de todas sus partes juntas.
