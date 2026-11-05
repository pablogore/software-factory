EPIC: Integraciones intercambiables: GitHub primero, chats que inician trabajo

## Why
La fábrica actual solo entiende issues y PRs de GitHub, y el control remoto por chat depende de una sesión de Claude prendida. Queremos pedir trabajo desde Telegram o Discord, recibir las preguntas y aprobaciones ahí, y poder usar GitLab o Jira más adelante sin reescribir el núcleo.

## Outcome
- Un `/ticket` en Telegram o Discord termina en un PR mergeado, con las preguntas, la spec y el escalado en el mismo hilo.
- El núcleo no importa ningún paquete de un proveedor: cambiar GitHub por GitLab es configuración.

## Out of scope
Slack y Linear (se agregan con el mismo patrón cuando se necesiten).

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/03-integraciones.md` (puertos, registry, config, reglas de entrada), `05-config.md`.

---
STORY: Puertos de integración y registry de proveedores
DEPENDS: E01.1

As a desarrollador de factoryd, I want definir EventSource, Channel, Tracker y CodeHost y un registry que arme la familia de cada proyecto, so that agregar un proveedor sea agregar un paquete.

## Acceptance criteria
1. Given los cuatro puertos de `03-integraciones.md`, when se compila, then existen en `internal/ports` con un fake en memoria cada uno y su suite de contrato.
2. Given un proveedor que se registra en `init()`, when la config de un proyecto lo pide para una capacidad que no tiene, then la validación falla con un mensaje claro.
3. Given la config del ejemplo de `03-integraciones.md`, when se arma la familia, then se obtienen los adaptadores pedidos para cada capacidad.

---
STORY: Proveedor GitHub completo
DEPENDS: 1, E02.3

As a operador de la fábrica, I want GitHub como EventSource, Tracker, CodeHost y Channel, so that factoryd haga todo lo que hoy hace la fábrica con `gh`.

## Acceptance criteria
1. Given un webhook de GitHub (issues, comentarios, reviews, check runs, push), when llega con firma válida, then se convierte en Trigger; con firma inválida, se rechaza.
2. Given que no hay webhook configurado, when corre el polling, then se obtienen los mismos Triggers sin duplicados.
3. Given las acciones del outbox (comentar, etiquetar, push, abrir y actualizar PR, mergear con SHA, cerrar issue), when se ejecutan, then usan la API REST y respetan el guard de rate limit.
4. Given la suite de contrato de los cuatro puertos, when se corre contra un GitHub falso (servidor HTTP de prueba), then pasa.
5. Given un token de una cuenta bot configurado, when la fábrica escribe en GitHub, then usa esa cuenta (cupo de API propio y autoría visible) y no la del dueño.

---
STORY: Telegram: pedir trabajo y responder desde el chat
DEPENDS: 1, E05.1

As a persona lejos de la computadora, I want mandar `/ticket` por Telegram y contestar las preguntas con botones, so that pueda mover trabajo desde el celular.

## Acceptance criteria
1. Given un `/ticket <texto>` de una persona de la allowlist de Telegram, when llega, then se crea el job y el bot responde "Recibido, job N" en un tema o cadena propia del job.
2. Given una pregunta de un agente o una spec a aprobar, when se publica, then llega al hilo del job con botones; al tocar un botón, la respuesta entra al job.
3. Given texto libre fuera del hilo de un job, when llega, then no ejecuta nada.
4. Given un id de Telegram, when se evalúan permisos, then nunca se traduce a una identidad de otro proveedor.

---
STORY: Discord: pedir trabajo y responder desde el chat
DEPENDS: 3

As a persona que usa Discord, I want lo mismo que en Telegram, so that el equipo use el chat que ya tiene.

## Acceptance criteria
1. Given un `/ticket` en el canal configurado, when lo manda alguien de la allowlist, then se crea el job y se abre un hilo de Discord para él.
2. Given preguntas, spec, PR listo, escalado y merge, when ocurren, then se avisan en ese hilo con botones donde corresponde.
3. Given la suite de contrato de Channel y EventSource, when se corre contra Discord y Telegram falsos, then ambos pasan la misma suite.

---
STORY: Issue espejo y suscripciones multi-hilo
DEPENDS: 2, 3, E05.6

As a equipo, I want que un ticket de chat deje registro en el tracker y que cualquier hilo del job sirva para dar feedback, so that no se pierda contexto.

## Acceptance criteria
1. Given `mirror_chat_tickets: true`, when se crea un job desde un chat, then se crea un issue espejo enlazado al hilo.
2. Given un job con hilo de chat, issue y PR, when una persona permitida escribe en cualquiera, then entra al job como feedback sin pasar por intake.
3. Given el merge, when termina, then se avisa una sola vez en el hilo de origen y se cierra el issue espejo.

---
STORY: GitLab como segundo code host y Jira como tracker
DEPENDS: 2

As a equipo que usa GitLab o Jira, I want usar factoryd igual, so that la fábrica no quede atada a GitHub.

## Acceptance criteria
1. Given la suite de contrato de CodeHost y EventSource, when se corre contra GitLab (servidor falso), then pasa: MR en vez de PR, mismos eventos de dominio.
2. Given la suite de contrato de Tracker, when se corre contra Jira (servidor falso), then pasa.
3. Given un proyecto con código en GitLab y tickets en Jira, when se arma la familia, then funciona sin cambios en el núcleo (test de wiring).
