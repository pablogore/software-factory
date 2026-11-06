EPIC: Workspaces aislados, gates y seguridad que no depende del agente

## Why
Hoy los permisos de los agentes son solo instrucciones en el prompt: un revisor podría escribir código y un builder pushear a cualquier rama. Con agentes intercambiables tampoco se puede depender de los hooks de uno. La seguridad tiene que estar en el sistema.

## Outcome
- Un agente no tiene credenciales de GitHub ni puede escribir fuera de su worktree.
- Un cambio que debilita tests o toca paths protegidos no llega a review.
- No quedan procesos, puertos ni worktrees huérfanos.

## Out of scope
Contenedores en máquinas remotas.

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/01-diseno.md` (seguridad agnóstica, gates), `07-arquitectura.md`.

---
STORY: Worktree, rama y puertos por job
DEPENDS: E01.1

As a operador de la fábrica, I want que cada job trabaje en su propio worktree y rango de puertos, so that varios jobs corran en paralelo sin pisarse.

## Acceptance criteria
1. Given un job nuevo, when arranca build, then existe un worktree propio sobre una rama `factory/<job-id>` creada desde la rama base configurada.
2. Given dos jobs a la vez, when piden puertos, then reciben rangos distintos (`base + step × n`, como hoy).
3. Given un dev server levantado para un job, when termina la etapa o el job, then el proceso y sus hijos terminan (process group).
4. Given un job mergeado o rechazado, when se limpia, then se borra el worktree y la rama local; si el worktree tiene cambios sin commitear, se guardan en un commit de respaldo antes de borrar.

---
STORY: Agentes sin credenciales y aislados
DEPENDS: 1, E03.1

As a dueño del repo, I want que los agentes no puedan pushear, comentar ni leer secretos, so that un agente equivocado o manipulado no pueda hacer daño fuera de su worktree.

## Acceptance criteria
1. Given un agente lanzado por factoryd, when se inspecciona su entorno, then no tiene tokens de GitHub, de chats ni de proveedores salvo el del modelo que usa.
2. Given un revisor, when intenta escribir en el worktree, then falla (montaje o permisos de solo lectura).
3. Given un agente, when intenta escribir fuera de su worktree o leer `.env`, claves SSH o `~/.config`, then falla.
4. Given un agente que pide permiso por ACP (`session/request_permission`), when lo pide para algo que su rol no permite, then factoryd lo niega.

## Notes
Empezar con un usuario de sistema por job o bubblewrap en Linux; documentar qué se usa en macOS. El aislamiento es configurable, pero el modo por defecto es aislado.

---
STORY: Gate determinista con checks del repo y reglas del diff
DEPENDS: 1

As a revisor humano, I want que lo que llega a review ya pasó los checks y no hace trampa, so that no pierda tiempo revisando algo roto.

## Acceptance criteria
1. Given un job en gate, when se corre, then ejecuta el comando de checks configurado en el worktree y guarda salida, código y duración.
2. Given un diff que borra tests, agrega `.skip`/`.only`/`t.Skip` o baja la cantidad de tests, when se corre el gate, then falla con el motivo.
3. Given un diff que toca un path protegido (configuración de la fábrica, `.factory/`, workflows) o archivos fuera de lo permitido para el rol, when se corre el gate, then falla con el motivo.
4. Given el gate falla, when el builder tiene ronda disponible, then recibe el log y no se lanzan revisores.

---
STORY: Specgate portado
DEPENDS: E01.3

As a persona que pidió una feature, I want que la spec sea corta y legible antes de construir, so that pueda aprobarla en dos minutos.

## Acceptance criteria
1. Given una spec, when se valida, then aplica los mismos límites que `factory/bin/specgate` en `main` (palabras, líneas, cantidad y largo de criterios, Valor, Demo, secciones opcionales, diagrama de hasta 15 líneas).
2. Given una spec con `## Excepción de largo`, when está dentro de los topes de excepción, then devuelve EXCEPTION y el job espera aprobación humana.
3. Given los casos de `factory/tests/test_specgate.py` y sus fixtures, when se portan, then pasan.

---
STORY: Re-correr checks sin agente antes del merge
DEPENDS: 3

As a dueño del repo, I want que antes de mergear se vuelvan a correr los checks sobre el commit exacto aprobado, sin ningún agente involucrado, so that ningún agente pueda aprobarse a sí mismo.

## Acceptance criteria
1. Given un job aprobado, when va a mergear, then factoryd sincroniza con la base, vuelve a correr los checks en un worktree limpio sobre el SHA revisado y solo mergea si pasan.
2. Given que la base avanzó y hay conflicto, when se sincroniza, then el job vuelve a build con el conflicto como entrada (no escala directo).
3. Given un push nuevo después de la aprobación, when se va a mergear, then se rechaza porque el SHA no es el revisado.

---
STORY: Doctor: detectar y limpiar restos
DEPENDS: 1

As a operador de la fábrica, I want un comando que encuentre lo que quedó colgado, so that la máquina no se llene de worktrees, ramas y procesos viejos.

## Acceptance criteria
1. Given worktrees o ramas `factory/*` de jobs cerrados, dev servers en puertos de la fábrica o sesiones de agentes sin job, when se corre `factory doctor`, then los lista con el motivo.
2. Given `factory doctor --fix`, when se corre, then los limpia, guardando antes un archivo de recuperación con las ramas y SHAs borrados.
3. Given trabajo sin mergear en un worktree, when se corre `--fix`, then no se borra: se reporta.

---
STORY: Sensores de trazabilidad y de fuentes citadas
DEPENDS: 3

As a revisor humano, I want que el gate verifique que cada criterio de aceptación tiene un test que lo cubre y que todo lo que un agente cita existe, so that ni un criterio quede sin probar ni pase una referencia inventada.

## Acceptance criteria
1. Given una spec con criterios numerados, when corre el gate, then cada criterio tiene al menos un test que lo nombra (por ejemplo `// AC2` o el nombre del spec en go-specs); si falta alguno, falla nombrando el criterio.
2. Given `build.md`, una review o la decisión del approver, when citan un SHA, un `archivo:línea`, un issue o un PR, then el gate verifica que exista (SHA en git, línea dentro del archivo, issue o PR en el tracker) y falla nombrando la cita que no existe.
3. Given una cita a un SHA, when el agente la escribe, then debe venir de `git rev-parse` o del contexto del job; una cita no verificable cuenta como hallazgo.

## Notes
Idea tomada de los sensors de AI-DLC (traceability, claim-sources). Motivado por un agente que citó un SHA inventado en otro proyecto.
