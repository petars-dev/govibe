# SPEC_HELLO

## Description
The hello feature of the fixture.

## Status Table

| Item | Implemented | Skipped | Why |
| --- | --- | --- | --- |
| `GET /api/hello` endpoint | Yes | | Served by `controllers.HelloAPI` |
| Landing page | Yes | | Rendered by `templates.Home` |
| MCP tool `fixture_ping` | Yes | | Registered by `devmcp.NewHandler` |
| Greeting history | No | Skipped | Out of scope: no `fixture_forget` tool either |
| Localised greetings | No | | Planned: `actions.Hello` returns one language |
| Settings `FIXTURE_DB_MAX_CONNS` | Partial | | Declared, not yet read |
