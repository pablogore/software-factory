# factoryd: una historia en paralelo (arquitecto + builders)

Fecha: 2026-11-06. Idea de Pablo: que la fábrica pueda decir "esta historia la encaramos con dos programadores y un integrador", y que el que arma el contrato sea el mismo que integra.

## Roles
- **Arquitecto**: un agente que decide si conviene paralelizar, escribe el **contrato** (tipos, puertos, interfaces y tests esqueleto) y, cuando las partes están listas, las **integra**. Es la misma sesión de principio a fin: se reanuda si el agente lo soporta y, si no, retoma con su propio `contract.md` como traspaso.
- **Builders** (dos o más): cada uno programa una parte contra el contrato, en su worktree, tocando solo los archivos de su parte.
- Después, todo sigue igual que en un job normal: gate completo, revisores sobre el diff total, approver y **un solo merge** a la base.

## Cuándo conviene
El arquitecto propone paralelizar solo si se cumplen las tres:
1. Las partes escriben archivos distintos (en esta arquitectura suele pasar: dominio, adaptador y slice viven en paquetes separados).
2. Se puede fijar un contrato antes, sin que dependa de lo que cada parte descubra.
3. La historia es grande: en una chica, coordinar cuesta más de lo que ahorra.

Si no, sigue el flujo normal. El plan (partes, archivos de cada una, por qué) se muestra para tu OK, o arranca solo con `auto`.

## Reglas
- El gate de cada parte rechaza un diff fuera de sus archivos; el del contrato, uno que agregue lógica más allá de tipos, firmas y tests esqueleto.
- Al integrar, el arquitecto solo escribe el pegamento (wiring, imports, adaptaciones mínimas). Si una parte no cumple el contrato, no la reescribe: le manda un rework solo a ese builder.
- El arquitecto no revisa su propio trabajo: los revisores ven el diff total con el contrato marcado, para que un contrato mal diseñado no pase porque "todo encaja".
- Costo: cada builder carga su contexto, así que gasta más tokens y menos tiempo de reloj. Los topes de #103 cuentan todo el trabajo de la historia. Una historia en paralelo ocupa tantos slots de concurrencia como builders.
- Solo factoryd: la fábrica actual no tiene rama de integración ni integrador.

## Diagrama
```mermaid
flowchart TD
  I["Historia"] --> AQ["Arquitecto<br/>¿conviene paralelizar?"]
  AQ -->|no| SEQ["Flujo normal<br/>un builder, slices en serie"]
  AQ -->|sí| PLAN["Plan paralelo<br/>partes, archivos de cada una<br/>y por qué"]
  PLAN --> OK{"Tu OK<br/>o auto"}
  OK --> C["Arquitecto escribe el contrato<br/>tipos, puertos, interfaces<br/>y tests esqueleto"]
  C --> IB[("Rama de integración")]
  IB --> B1["Builder 1<br/>programa su parte"]
  IB --> B2["Builder 2<br/>programa su parte"]
  B1 --> G1["Gate parte 1<br/>diff solo en sus archivos"]
  B2 --> G2["Gate parte 2<br/>diff solo en sus archivos"]
  G1 --> J{"Las dos READY"}
  G2 --> J
  J --> INT["El mismo arquitecto integra<br/>mergea las partes y el pegamento<br/>sin reescribir lo de los builders"]
  INT -->|una parte no cumple el contrato| RW["Rework solo a ese builder"]
  RW --> J
  INT --> GT["Gate completo"]
  GT --> REV["Revisores sobre el diff total"]
  REV --> AP["Approver"]
  AP --> M["Un solo merge a la base"]
```
