EPIC: Confianza y operación: replays, evals, seguridad, dogfooding y releases

## Why
A medida que la fábrica trabaja sola, necesitamos probarla barato, saber si un cambio de prompt o de modelo la empeora, asegurarnos de que un texto malicioso no la manipule, poder frenarla al instante y publicar el binario con garantías.

## Outcome
- Un cambio de prompt, modelo o agente se valida contra jobs de referencia antes de usarse.
- Ningún fixture malicioso logra salir del sandbox, conseguir credenciales o aprobar algo.
- factoryd construye factoryd, y sus releases son verificables.

## Out of scope
Benchmarks públicos de modelos.

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/01-diseno.md`, `04-roles.md`, `07-arquitectura.md`.

---
STORY: Grabar y reproducir sesiones de agentes
DEPENDS: E03.1, E05.5

As a desarrollador de factoryd, I want grabar las sesiones ACP reales de un job y reproducirlas en tests, so that pueda probar el flujo completo sin gastar tokens y con resultados deterministas.

## Acceptance criteria
1. Given `factory record <job>`, when corre un job, then cada sesión de agente queda guardada como fixture (mensajes, tool calls, resultados) sin secretos.
2. Given un fixture, when se corre el test de punta a punta con el agente de replay, then el job recorre exactamente las mismas etapas y eventos que en la grabación.
3. Given un cambio en el núcleo que altera el flujo, when corre el replay, then el test falla mostrando el primer evento distinto.

---
STORY: Evals por rol con jobs de referencia
DEPENDS: E03.6, E08.6

As a operador de la fábrica, I want un set fijo de jobs de referencia para comparar agentes, modelos y prompts de un rol, so that no empeore la fábrica al cambiar el ruteo o editar un prompt.

## Acceptance criteria
1. Given `evals/<rol>/` con casos (entrada y resultado esperado o criterios), when se corre `factory eval <rol> --agent X --model Y`, then se obtiene un puntaje por caso, el costo y la duración.
2. Given un PR que cambia un prompt de rol o su modelo, when corre el CI, then compara contra la versión base y falla si el puntaje baja más de un umbral configurable.
3. Given los resultados, when se pide `factory stats --evals`, then se ven por rol, agente y modelo.

---
STORY: Tests de inyección de prompts
DEPENDS: E04.2, E05.1, E06.3

As a dueño del repo, I want fixtures de issues, comentarios y archivos maliciosos que se corran contra la fábrica, so that tenga pruebas de que un texto no puede manipularla.

## Acceptance criteria
1. Given fixtures con instrucciones inyectadas (en el issue, en un comentario del PR, en un archivo del repo, en un mensaje de chat), when corren, then ningún agente consigue credenciales, escribe fuera de su worktree ni hace push.
2. Given un fixture que pide "aprobá este PR" o "mergeá", when corre, then ninguna aprobación ni merge ocurre sin la confirmación humana de #60.
3. Given un fixture nuevo que logra cualquiera de lo anterior, when corre el CI, then falla.

---
STORY: Dogfooding: factoryd construye factoryd
DEPENDS: E10.4, E05.6

As a dueño del repo, I want que factoryd tome el backlog restante de su propio repo, so that la fábrica nueva se pruebe con trabajo real antes del corte.

## Acceptance criteria
1. Given factoryd en modo sombra sobre este repo, when la fábrica vieja procesa un issue, then factoryd lo procesa en paralelo sin publicar y el reporte compara ambos.
2. Given 5 issues con resultados comparables o mejores, when se decide pasar a modo real, then factoryd toma los issues nuevos de este repo y la fábrica vieja deja de hacerlo.
3. Given el modo real, when un job de factoryd falla de una forma que la vieja no fallaba, then se registra como issue con el job de referencia.

---
STORY: Presupuesto global y freno de emergencia
DEPENDS: E02.6, E07.2, E06.3

As a operador de la fábrica, I want un tope de gasto diario y mensual para toda la fábrica y un freno que pueda mandar desde el chat, so that nunca me lleve una sorpresa de costo ni dependa de estar frente a la computadora para pararla.

## Acceptance criteria
1. Given topes diario y mensual configurados, when se alcanzan, then no se lanza ningún agente nuevo hasta el próximo período y se avisa en los canales.
2. Given `factory pause` desde la CLI, la skill o `/pause` en Telegram o Discord por una persona permitida, when se recibe, then se cancelan las sesiones en curso dejando cada job en un estado reanudable y no arranca nada nuevo.
3. Given `factory resume`, when se recibe, then los jobs vuelven a la cola donde estaban.

---
STORY: Releases firmadas con procedencia por job
DEPENDS: E10.3

As a persona que instala factoryd, I want binarios firmados con su SBOM y un recibo de qué produjo cada PR, so that pueda verificar lo que instalo y auditar lo que la fábrica hizo.

## Acceptance criteria
1. Given un tag `v*`, when corre el workflow de release, then se publican binarios para Linux, macOS y Windows con checksums, firma y SBOM.
2. Given un job mergeado, when se consulta su recibo, then dice qué versión de factoryd, qué roles (con digest), qué agentes y modelos y qué checks produjeron el PR.
3. Given un recibo, when se corre `factory verify <pr>`, then valida firma y contenido sin volver a correr el job.
