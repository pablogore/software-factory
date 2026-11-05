EPIC: Extensiones, migración y corte de la fábrica vieja

## Why
Para reemplazar a la fábrica actual hace falta poder hacer lo que hoy no encaja en los roles fijos, reutilizar roles entre proyectos con control de versiones, y pasar de una fábrica a la otra sin perder trabajo.

## Outcome
- factoryd procesa issues reales en paralelo a la fábrica vieja con resultados comparables, y luego la reemplaza.
- Instalar factoryd en un repo nuevo es un comando.

## Out of scope
Ejecución distribuida en varias máquinas.

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/04-roles.md` (kind task, catálogo), `01-diseno.md` (fases).

---
STORY: Kind task con archivos declarados y checks
DEPENDS: E05.4, E08.6

As a operador de la fábrica, I want un tipo de rol genérico que produzca archivos verificados (documentación, changelog, una migración propuesta), so that no tenga que escribir código Go para cada tarea nueva.

## Acceptance criteria
1. Given un rol `kind: task` con `produces` (archivos) y `verify` (comandos), when corre, then la etapa solo termina si todos los archivos existen y todos los comandos pasan.
2. Given un `verify` que falla, when hay presupuesto de reintentos, then el agente recibe el error y reintenta; sin presupuesto, needs-human.
3. Given un pipeline con una etapa task después de approve (por ejemplo "actualizar changelog"), when corre un job, then se ejecuta y su resultado queda en el PR.

---
STORY: Catálogo de roles importables con versión fijada
DEPENDS: 1

As a equipo con varios repos, I want importar roles desde un repo compartido con versión fija, so that reutilice revisores sin copiar y pegar.

## Acceptance criteria
1. Given `factory role add github:org/roles//security-deep@v1.2`, when se ejecuta, then descarga esa versión, la valida y propone el cambio como PR al repo de configuración.
2. Given un rol importado, when corre en un job, then el historial registra origen, versión y digest.
3. Given una versión nueva disponible, when existe, then factoryd nunca actualiza solo; `factory role outdated` la lista.

---
STORY: Instalador y onboarding
DEPENDS: E07.4, E08.1

As a persona que quiere usar factoryd en su repo, I want instalarlo y configurarlo con un comando, so that no tenga que leer toda la documentación para empezar.

## Acceptance criteria
1. Given un repo sin factoryd, when se corre `factory init`, then detecta el lenguaje y propone comandos de checks, crea `.factory/` con defaults, pide qué proveedores usar y valida todo.
2. Given `factory init`, when termina, then instala la skill `factory` en los agentes detectados (Claude Code, OpenCode, Pi) y explica en 10 líneas cómo arrancar.
3. Given una máquina sin factoryd corriendo, when se ejecuta `factory service install`, then queda como servicio del usuario (systemd o launchd).

---
STORY: Modo sombra contra la fábrica vieja
DEPENDS: E05.6, E06.2

As a dueño del repo, I want correr factoryd sobre los mismos issues que la fábrica vieja sin que publique nada, so that compare resultados antes de cambiar.

## Acceptance criteria
1. Given modo sombra, when factoryd procesa un issue, then hace todo salvo efectos externos: el outbox registra lo que habría hecho sin hacerlo.
2. Given un issue procesado por las dos, when se pide el reporte, then compara etapas, rondas, veredictos, costo y duración.
3. Given 10 issues comparados, when se revisa el reporte, then queda documentado en el PR si factoryd está lista para el corte o qué falta.

---
STORY: Corte: factoryd reemplaza a la fábrica vieja
DEPENDS: 4, 3

As a dueño del repo, I want pasar de la fábrica vieja a factoryd sin perder jobs en curso, so that quede una sola fábrica.

## Acceptance criteria
1. Given jobs abiertos en la fábrica vieja, when se corre `factory migrate`, then se importan sus estados, iniciativas y slices a factoryd o se listan los que hay que terminar en la vieja.
2. Given el corte hecho, when se mergea esta rama a `main`, then el README y los docs explican factoryd y la fábrica vieja queda en un tag `legacy-v3`.
3. Given el corte, when se revisa el repo, then ya no existe orquestación en prompts: SKILL.md del orquestador viejo y `factory/bin/job` no están.

---
STORY: Product owner y planificación de sprint en factoryd
DEPENDS: E08.6, E07.2

As a persona con una idea de negocio, I want contársela a la fábrica y que la convierta en una épica con historias y un sprint priorizado, so that no tenga que escribir issues a mano.

## Acceptance criteria
1. Given `factory idea "<texto>"` (o lo mismo desde la skill o un canal), when corre el rol product-owner, then propone una épica con 2 a 6 historias en el formato EPIC/STORY/DEPENDS y la muestra para confirmar antes de crear nada.
2. Given la confirmación, when se crean, then quedan como épica con lista de tareas e historias con "Part of epic" y "Depends on", sin encolar.
3. Given `factory sprint --capacity N`, when corre el product-owner sobre el backlog, then propone qué historias entran y en qué orden; al confirmar, se encolan en ese orden.
4. Given paridad con `factory/bin/stories` en `main`, when se usa el mismo archivo de historias, then el resultado en GitHub es equivalente.
