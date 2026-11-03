# Contenido

- `.claude/` (carpeta oculta): agentes, skill `/factory` y settings. En el celular puede no verse; está.
- `factory/`: scripts (`bin/`), config, checks, dashboard y tests.
- `V3.md`: qué cambió respecto de la v2.
- `docs/notas/plan-mejoras.md`: análisis y plan completo (feedback, dashboard, Git Flow, tokens, otros).
- `docs/notas/v2-a-v3.patch`: todos los cambios contra la v2.
- `.git/`: historial con dos commits (v2 base → v3).

Instalar en tu repo: copiar `.claude/` y `factory/` a la raíz, seguir `INSTALL.md`, y correr
`python3 -m unittest discover factory/tests`.
