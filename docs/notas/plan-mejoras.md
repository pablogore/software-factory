# Plan de mejoras para software-factory v2

Fecha: 2026-11-03. Base: `src/software-factory-claude-code-v2` (SKILL.md, factory/bin/job, agentes).

## 1. Rework con feedback humano sobre el PR (bug principal)
Problemas encontrados:
- El feedback solo entra con `/factory <texto>` en la sesión de Claude. Los comentarios y reviews del PR en GitHub no se leen; `job queue` muestra `needs-human` siempre como `wait` (para `needs-info` sí existe `replied_since_question`).
- `cmd_feedback` solo acepta jobs en `needs-human`. Con APPROVE el merge es automático y no hay instancia de rework.
- En la ronda de feedback solo el builder lee `feedback-k.md`. Los revisores no lo leen y revisan contra el `spec.md` original (piden revertir). El spec no se actualiza. El approver exige "dentro del Scope de intake" y reaplica las reglas de escalado → puede re-escalar lo mismo (loop).

Arreglos:
- `job queue`: detectar comentarios/reviews humanas en el PR posteriores a `decision.md` → `resume`. `job feedback` los toma de `gh pr view --json comments,reviews`.
- Ronda de feedback: el spec-writer agrega a `spec.md` una sección "Cambios por feedback k".
- Revisores y approver leen `feedback-*.md`; el approver no re-escala decisiones que el humano ya tomó.

## 2. Dashboard casi en tiempo real
- MCP no es lo adecuado (sirve para que el agente use herramientas).
- Servidor local chico que vigile `factory/jobs/` y empuje cambios por SSE (o WebSocket) al dashboard, en vez de polling.
- Hooks de Claude Code (SubagentStart/SubagentStop, PostToolUse) que emitan eventos al servidor para ver agentes arrancar/terminar.
- Alternativa si GitHub pasa a ser el bus (punto 3): webhooks de GitHub como fuente de eventos.

## 3. Git (y GitHub) como canal de comunicación + Git Flow
Hoy: rama y PR por job, labels e issue comments; pero el estado y los informes de los agentes viven en `factory/jobs/` (gitignored, local).

Propuesta:
- Lo que un humano debe ver o contestar va a GitHub: spec como comentario o archivo en la rama, cada revisor publica una PR review real (APPROVE / REQUEST_CHANGES), decision del approver como review. Humanos y agentes conversan en el mismo lugar → resuelve también el punto 1.
- Estado del job derivado de labels/estado del PR (`factory:building`, `factory:review`, ...). `job.json` queda como caché local.
- Local solo lo efímero: puertos, sesiones, costo, logs largos.
- Git Flow: la fábrica mergea `factory/<id>` → `develop` (con `base_branch: develop` en config.json ya se logra casi todo). `develop → main` vía `release/*` solo lo hace un humano (o un agente de release que siempre escala). Hotfixes desde `main`.
- Riesgos: latencia y rate limits de la API de GitHub, ruido en los PRs, más llamadas `gh` por transición.

## 4. Economía de tokens y contexto
Dónde se va el gasto hoy (v2):
- `intake` corre en **fable** (el modelo más caro) para cada issue, aunque es una clasificación.
- spec-writer, builder y approver en opus fijo, sin importar el riesgo.
- Cada revisor arranca de cero y re-explora el repo (AGENTS.md, spec, build.md, diff, archivos vecinos): costo de orientación × revisores × rondas.
- Cada ronda re-corre todos los revisores, aunque solo uno pidió CHANGES.
- El approver lee todo: todas las rondas y el diff completo.
- El builder reanudado acumula contexto ronda tras ronda.
- Con suscripción, los subagentes consumen el mismo límite: `concurrency: 3` × hasta 4 revisores en paralelo lo agota rápido.

Medidas (de mayor a menor ahorro/esfuerzo):
1. Ruteo de modelos por riesgo también para intake/spec/approver: intake en haiku/sonnet; opus solo en riesgo alto.
2. Rondas ≥2: re-revisar solo los lentes con CHANGES (+ code), y pasarles solo el diff de la ronda (`git diff <commit-anterior>..HEAD`).
3. "Context pack" por job generado por script (archivos relevantes, diffstat, resultado de Graphify) que leen todos los revisores en vez de explorar cada uno; prefijo idéntico entre revisores paralelos para aprovechar prompt caching.
4. Approver: solo la ronda final + Summaries, no todo el historial.
5. Presupuesto por job en config.json (tokens o USD equivalentes); `job advance` escala a needs-human si se pasa. Mostrarlo en el dashboard.
6. Builder nuevo (no reanudado) pasadas 2 rondas, con build.md como traspaso, para cortar el crecimiento del contexto.
7. En suscripción: `concurrency: 1–2`.

Herramientas:
- **Graphify**: grafo del repo (código vía tree-sitter, sin tokens para construirlo; docs/imágenes sí cuestan). Ataca la orientación, que es justo el costo repetido de los revisores. El grafo se puede commitear y regenerar tras cada commit (encaja con el punto 3). Ganancia real reportada 5–10× en repos reales (el 71.5× es en corpus mixto); en repos chicos aporta poco.
- **Engram** (Gentleman-Programming, SQLite + FTS5 vía MCP): memoria entre jobs, no dentro de un job (ahí los archivos ya son la memoria). Útil para aprendizajes recurrentes (cómo levantar el dev server, hallazgos que los revisores repiten). Riesgo: memoria contaminada y que el recall agregue tokens a cada prompt.
- Alternativa sin dependencias para empezar: `factory/learnings.md` versionado en git, que el approver actualiza al cerrar cada job y que AGENTS.md referencia.

## 5. Otros agregados
1. **Permisos forzados, no solo de prompt.** La v2 no trae `.claude/settings.json`; revisores, spec-writer y approver tienen Bash + Write. Agregar hooks PreToolUse: cada agente escribe solo su archivo, `git push` solo a `factory/<job-id>`, nada de `gh pr merge` fuera de `job merge`. Crítico con el control remoto por Discord.
2. **Seguridad del nuevo canal de feedback.** Si los comentarios del PR pasan a ser instrucciones (punto 1), aceptar solo los de autores con permiso de escritura (`authorAssociation` OWNER/MEMBER/COLLABORATOR) y pasarlos como dato citado, igual que `issue.md`.
3. **Tests protegidos de forma determinista.** `protected_paths` solo cubre checks.sh y config.json; que un test fue debilitado lo juzga un revisor. Sumar al gate: detectar tests borrados o `.skip`/`.only`, y que la cobertura no baje.
4. **Preview para el humano.** Para dar feedback hay que ver la feature andando: deploy de preview por PR (Vercel/Netlify/Cloudflare) o, como mínimo, que ux/ui adjunten screenshots al PR.
5. **Conflictos entre jobs concurrentes.** Hoy el sync con la base se hace recién en `job merge`, y si hay conflicto va directo a needs-human. Mejor: cuando un job mergea, avisar a los otros jobs abiertos y que el builder haga rebase/resuelva antes de escalar.
6. **Issues grandes → sub-issues.** Que intake pueda partir un issue grande en sub-issues encadenados en vez de un job enorme (mejor calidad y menos contexto).
7. **Limpieza y salud.** `job doctor`: worktrees huérfanos, dev servers en puertos de la fábrica, sesiones de agent-browser y ramas remotas de jobs cerrados.
8. **Tests de la propia fábrica.** `factory/bin/job` y `gate` son el núcleo y no tienen tests. Un repo fixture + `gh` simulado para probar transiciones, y modo dry-run.
9. **Métricas para afinar.** Por job: rondas, % de aprobación en ronda 1, tasa de rework humano, costo y tokens por agente. Sirve para decidir el ruteo de modelos del punto 4 con datos.

## Estado en v3
Hechos: 1 (rework), 4.1/4.2/4.4 (tokens), 5.1 (guard), 5.2 (seguridad del feedback), 5.6 parcial (el tech-lead propone splits), 5.8 (tests con gh simulado). Más: agentes tech-lead y product-owner/PM.
Pendientes (para la versión en Go): 2, 3 completo, 4.3/4.5/4.6, 5.3, 5.4, 5.5, 5.7, 5.9.
