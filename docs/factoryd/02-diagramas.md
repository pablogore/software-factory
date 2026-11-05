# factoryd: diagramas del flujo

### 1. Arquitectura
```mermaid
flowchart LR
  subgraph EXT["Afuera"]
    GH["GitHub<br/>issues, PRs, checks"]
    DC["Discord"]
    UI["Dashboard"]
  end
  subgraph FD["factoryd (Go)"]
    IN["Ingreso de eventos<br/>webhooks + polling"]
    SM["Máquina de estados"]
    DB[("SQLite<br/>jobs, eventos, costos")]
    SCH["Scheduler<br/>slots, presupuesto"]
    WS["Workspaces<br/>worktrees, puertos, sandbox"]
    GT["Gates deterministas"]
    OB["Outbox<br/>push, PR, comentarios"]
    RUN["Runner<br/>interfaz Agent"]
    MCP["Servidor MCP"]
    API["API + SSE"]
  end
  subgraph AG["Agentes, intercambiables por rol"]
    CL["Claude Code"]
    CX["Codex"]
    GE["Gemini CLI"]
  end
  GH -->|webhooks| IN
  IN --> SM
  SM <--> DB
  SM --> SCH
  SCH --> RUN
  SM --> WS
  SM --> GT
  SM --> OB
  OB --> GH
  OB --> DC
  DC -->|comandos| API
  API --> SM
  API -->|eventos en vivo| UI
  RUN -->|ACP| CL
  RUN -->|ACP| CX
  RUN -->|ACP| GE
  CL -->|MCP| MCP
  CX -->|MCP| MCP
  GE -->|MCP| MCP
  MCP --> SM
```

### 2. Vida de un job
```mermaid
stateDiagram-v2
  [*] --> intake: issue con label factory
  intake --> needs_info: falta información
  needs_info --> intake: el humano responde
  intake --> spec
  spec --> specgate
  specgate --> spec: rechazada, reintento
  specgate --> needs_human: excepción de largo
  specgate --> build: OK
  build --> gate
  gate --> build: falla, dentro del presupuesto
  gate --> needs_human: presupuesto agotado

  state fork_review <<fork>>
  state join_review <<join>>
  gate --> fork_review: OK
  fork_review --> security
  fork_review --> ux
  fork_review --> ui
  fork_review --> code
  security --> join_review
  ux --> join_review
  ui --> join_review
  code --> join_review

  state veredicto <<choice>>
  join_review --> veredicto
  veredicto --> build: algún CHANGES
  veredicto --> needs_human: rondas agotadas
  veredicto --> approve: todos PASS

  approve --> merge: APPROVE y checks sin agente OK
  approve --> needs_human: ESCALATE
  needs_human --> build: feedback en el PR
  needs_human --> merge: el humano aprueba
  merge --> done
  done --> build: comentario humano reabre
  done --> [*]
```

### 3. Una etapa por dentro (builder)
```mermaid
sequenceDiagram
  autonumber
  participant F as factoryd
  participant W as Workspace
  participant A as Agente vía ACP
  participant M as MCP de factoryd
  participant G as GitHub
  F->>W: crea worktree, puerto y sandbox sin credenciales
  F->>A: session/new con cwd, servidores MCP y token del job
  F->>A: session/prompt con el prompt del rol
  A->>M: context_pack
  M-->>A: spec, diff, decisiones, learnings
  A->>F: session/request_permission
  F-->>A: permite o niega según el rol
  A->>A: edita código en su worktree
  A->>M: run_gate
  M-->>A: resultado de tests, lint y build
  A->>M: submit_result con STATUS READY
  M->>F: valida contra el schema del rol
  F->>F: revisa archivos tocados y re-corre checks
  F->>G: commit, push y PR desde el outbox
  F->>F: avanza el job a review
```
