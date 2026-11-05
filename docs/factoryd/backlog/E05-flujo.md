EPIC: Flujo completo de un job, de ticket a merge

## Why
Con el núcleo, la persistencia, los agentes y los workspaces listos, falta lo que el usuario ve: que un pedido se convierta en un PR mergeado pasando por spec, build, revisores y approver, como hoy pero sin depender de una sesión de Claude prendida.

## Outcome
- Un issue con la etiqueta de la fábrica termina mergeado (o escalado con un motivo claro) sin intervención, con factoryd corriendo solo.
- Agregar o sacar un revisor del pipeline no requiere tocar los slices.

## Out of scope
Canales de chat y otros proveedores (épica de integraciones); se usa GitHub o un proveedor falso.

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/02-diagramas.md`, `04-roles.md`, `07-arquitectura.md` (estructura de slices).

---
STORY: Slice ticket: de un pedido a un job
DEPENDS: E02.1, E02.2

As a persona que pide trabajo, I want que mi pedido se convierta en un job una sola vez, so that no haya trabajo duplicado ni pedidos de desconocidos.

## Acceptance criteria
1. Given un Trigger de tipo comando o un issue con la etiqueta de la fábrica, when lo envía una persona de la allowlist de ese proveedor, then se crea un job con su origen (proveedor, hilo, autor) y se confirma en el hilo una sola vez.
2. Given el mismo Trigger dos veces, when llega, then no se crea un segundo job.
3. Given un autor fuera de la allowlist, when pide trabajo, then no se crea nada y queda registrado como descartado.
4. Given el texto del pedido, when se pasa a los agentes, then va citado como dato, nunca como instrucción.

---
STORY: Slices intake y spec con specgate
DEPENDS: 1, E03.2, E04.4

As a persona que pidió una feature, I want que la fábrica entienda el pedido, me pregunte si falta algo y me muestre una spec corta, so that lo que se construya sea lo que pedí.

## Acceptance criteria
1. Given un job nuevo, when corre intake, then queda ACCEPT (con riesgo y superficies), NEEDS-INFO (con la pregunta) o REJECT (con el motivo).
2. Given ACCEPT, when corre el spec-writer, then la spec pasa por specgate; con hasta dos reintentos; al tercer rechazo, needs-human.
3. Given un issue grande, when el spec-writer propone una iniciativa, then se crean los slices y arranca el primero.
4. Given riesgo y superficies de intake, when se arma la ronda de review, then los roles con `when` que no aplican quedan salteados y registrados como tales.

---
STORY: Slices build y gate
DEPENDS: 2, E04.1, E04.3

As a persona que pidió una feature, I want que la fábrica construya en su worktree y no mande a revisar algo que no pasa los checks, so that la review sea sobre algo que funciona.

## Acceptance criteria
1. Given una spec aprobada, when corre build, then el builder trabaja en el worktree del job y entrega READY o BLOCKED con resumen.
2. Given READY, when corre el gate y falla, then el builder recibe el log en la misma ronda (si quedan intentos) o el job escala.
3. Given READY y gate OK, when termina, then factoryd hace commit, push y abre (o actualiza) el PR en borrador vía outbox; el agente nunca pushea.
4. Given un builder que se reanuda en la ronda siguiente, when el agente no soporta reanudar sesión, then se lanza uno nuevo con el resumen anterior como traspaso.

---
STORY: Slice review con roles dinámicos y modos
DEPENDS: 3, E03.3

As a operador de la fábrica, I want que la ronda de review corra los roles del pipeline en paralelo con sus modos, so that pueda sumar un revisor nuevo sin riesgo.

## Acceptance criteria
1. Given la etapa review del pipeline, when arranca la ronda, then corre en paralelo cada rol cuyo `when` aplica, con su agente, modelo, permisos y presupuesto.
2. Given un rol en shadow, when termina, then sus hallazgos se guardan pero no se publican ni cuentan; en advisory se publican en el PR pero no bloquean; en blocking cuentan para el veredicto.
3. Given hallazgos de la ronda anterior, when un revisor vuelve a correr, then los recibe y cada uno queda marcado como resuelto o vigente con prueba (archivo:línea).
4. Given cada revisor que termina, when publica en el PR, then lo hace como comentario o review con su nombre de rol, vía outbox.

---
STORY: Slices approve y merge
DEPENDS: 4, E04.5

As a dueño del repo, I want que el approver decida con todos los resultados y que el merge solo ocurra sobre lo revisado, so that lo que entra a la base esté verificado.

## Acceptance criteria
1. Given una ronda sin CHANGES bloqueantes, when corre el approver, then recibe la lista genérica de resultados (rol, modo, veredicto, hallazgos) y devuelve APPROVE o ESCALATE con motivos.
2. Given APPROVE y la política de merge lo permite, when se mergea, then usa el SHA revisado y los checks sin agente; given una política que exige humano para ese tipo de cambio (auth, pagos, migraciones, CI, dependencias nuevas), then pasa a needs-human aunque el approver haya aprobado.
3. Given ESCALATE, when se registra, then el PR queda listo para revisión con la decisión como comentario y la etiqueta needs-human.
4. Given un merge exitoso, when termina, then se limpian worktree y rama, se cierra el issue si corresponde y, si es una iniciativa, corre el replan.

---
STORY: Slice feedback: comentarios humanos que reanudan
DEPENDS: 5, E01.5

As a persona que revisa el PR, I want que mi comentario en el PR o en el chat haga que la fábrica corrija, so that no tenga que usar otra herramienta para pedir cambios.

## Acceptance criteria
1. Given un job escalado, when una persona con permiso de escritura comenta en el PR, then el comentario entra como feedback, se agrega a la spec como "Cambios por feedback" y el job vuelve a build.
2. Given ese feedback, when corren los revisores y el approver, then lo leen y no vuelven a escalar una decisión que la persona ya tomó.
3. Given un comentario de alguien sin permiso de escritura, o de un bot, when llega, then no entra como instrucción.
4. Given un comentario `[direction]`, when llega, then va al replan de la iniciativa en vez de al builder.
