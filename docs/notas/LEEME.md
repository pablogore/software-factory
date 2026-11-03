# Contenido

- `.claude/` (carpeta oculta): agentes, skill `/factory` y settings. En el celular puede no verse; está.
- `factory/`: scripts (`bin/`), config, checks, dashboard y tests.
- `V3.md`: qué cambió respecto de la v2.
- `docs/notas/plan-mejoras.md`: análisis y plan completo (feedback, dashboard, Git Flow, tokens, otros).
- `docs/notas/v2-a-v3.patch`: todos los cambios contra la v2.
- `.git/`: historial con dos commits (v2 base → v3).

Instalar en tu proyecto:

    ./install.sh /ruta/a/tu/proyecto              # base = rama por defecto de origin
    ./install.sh /ruta/a/tu/proyecto --base develop   # Git Flow
    ./install.sh /ruta/a/tu/proyecto --dry-run    # ver qué haría
    ./install.sh /ruta/a/tu/proyecto --force      # actualizar (respalda lo que pisa)

No pisa tu `factory/config.json` ni tu `factory/checks.sh`, no commitea ni pushea.
