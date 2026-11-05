EPIC: Configuración como código y roles dinámicos

## Why
Hoy la configuración es un JSON local y agregar un revisor nuevo implica escribir un agente y tocar el orquestador. Queremos versionar la configuración en git, sincronizarla con GitHub, y sumar roles (por ejemplo, un revisor experto en seguridad) sin código y sin riesgo.

## Outcome
- La configuración vigente siempre corresponde a un commit, y cada job registra con cuál corrió.
- Un rol nuevo se agrega con un YAML y un prompt, arranca en modo shadow y se promueve con datos.

## Out of scope
El catálogo de roles importables de terceros (épica final).

## Contexto
Rama base `arch/factoryd-go`. Diseño: `docs/factoryd/05-config.md`, `04-roles.md`.

---
STORY: Schema, validate, plan y apply de la configuración
DEPENDS: E01.2

As a operador de la fábrica, I want validar y ver el efecto de un cambio de configuración antes de aplicarlo, so that no rompa la fábrica con un typo.

## Acceptance criteria
1. Given los archivos de `05-config.md` (factory, providers, roles, gates, secrets) en sus cuatro capas, when se corre `factory config validate`, then se verifica el schema y que existan los agentes, proveedores, roles y secretos referidos.
2. Given un cambio, when se corre `factory config plan`, then se ve el diff de lo que cambiaría, marcando los cambios sensibles.
3. Given `factory config apply`, when se aplica, then factoryd recarga sin reiniciar; los jobs en curso siguen con su versión y los nuevos usan la nueva.
4. Given cualquier job, when se mira su historial, then figura el commit y el digest de configuración con que corrió.

---
STORY: Exportar e importar la configuración
DEPENDS: 1

As a operador de la fábrica, I want exportar mi configuración e importarla en otra máquina, so that pueda replicar o respaldar la fábrica.

## Acceptance criteria
1. Given `factory config export --out dir` o `--tar file`, when se ejecuta, then se escriben los archivos de configuración sin ningún valor secreto (solo referencias) y sin la capa local salvo `--include-local`.
2. Given `factory config import <dir|tar|url-git>`, when se ejecuta, then siempre muestra validate y plan antes de aplicar, y pide confirmación si hay cambios sensibles.
3. Given un export, when se lo importa en una fábrica vacía, then la configuración resultante es idéntica (test de ida y vuelta).

---
STORY: Secretos por referencia
DEPENDS: 1

As a dueño del repo, I want que los tokens nunca estén en los archivos de configuración, so that pueda versionarlos y compartirlos sin filtrar nada.

## Acceptance criteria
1. Given una referencia `secret://<nombre>`, when factoryd la necesita, then la resuelve desde el llavero del sistema o variables de entorno (en ese orden, configurable).
2. Given una referencia que no se puede resolver, when se valida, then el error nombra la referencia sin mostrar ningún valor.
3. Given logs, eventos y exports, when se revisan, then no contienen ningún valor secreto (test que busca los valores de prueba en todas las salidas).

---
STORY: Sincronizar desde un repo de configuración en GitHub
DEPENDS: 2, E06.2

As a equipo, I want que la configuración viva en un repo y que factoryd la tome de ahí, so that los cambios se revisen como código.

## Acceptance criteria
1. Given un repo de configuración configurado, when llega un push (webhook o polling), then factoryd valida, planifica y aplica si no hay cambios sensibles.
2. Given un cambio sensible en ese push, when llega, then queda pendiente de aprobación humana y aparece en el inbox y en los canales.
3. Given que lo aplicado no coincide con el commit remoto, when se corre `factory config sync status`, then lo reporta como drift.

---
STORY: Cambios desde la conversación como PR al repo de configuración
DEPENDS: 4, E07.3

As a persona que opera desde su agente o el chat, I want pedir un cambio de configuración y que se convierta en un PR, so that git siga siendo la única fuente de verdad.

## Acceptance criteria
1. Given "cambiá el modelo del reviewer-code a X" desde la CLI, la skill o un canal, when se confirma, then factoryd abre un PR al repo de configuración con el diff y el plan en la descripción, sin aplicar nada.
2. Given que ese PR se mergea, when llega el push, then se aplica por el camino del pull.
3. Given que los agentes de la fábrica intentan modificar el repo de configuración o `.factory/`, when lo hacen, then el gate lo rechaza.

---
STORY: Roles dinámicos con modos y métricas
DEPENDS: 1, E05.4

As a operador de la fábrica, I want agregar un rol nuevo con YAML y prompt, verlo en shadow y promoverlo con datos, so that sume revisores sin riesgo.

## Acceptance criteria
1. Given `factory role add roles/security-deep.yaml`, when se ejecuta, then pasa por validate, plan y PR como cualquier cambio sensible.
2. Given `factory config plan` con el rol nuevo, when se muestra, then incluye el impacto estimado: en cuántos de los últimos N jobs habría corrido según su `when` y el costo estimado.
3. Given un rol en shadow con jobs corridos, when se pide `factory stats --role security-deep`, then se ven costo, hallazgos, hallazgos que terminaron en cambios reales y falsos positivos marcados por personas.
4. Given el ejemplo de `04-roles.md`, when se agrega y se corre un job que toca `src/auth/`, then el rol corre; si el job no toca nada de su `when`, then queda salteado sin costo.
