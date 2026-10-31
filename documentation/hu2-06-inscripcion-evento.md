# HU2-06 — Inscripción al torneo (parte Pagos)

Parte de **Pagos** lista sobre las tablas de T2-03 (`events`, `event_entries`, `wallets`, `ledger_entries`) y alineada con `documentation/orquestador-api.md` (§3.5). Faltan la ruta del **Orquestador** (`POST /api/eventos/{id}/inscripcion`), las fichas en **Redis** y el **Front**.

## Reglas (prototipo 2)

| Regla | Dónde vive | Valor tentativo |
| --- | --- | --- |
| Inscripción en PoleCoins, va al botín | `events.buy_in` (por defecto `EVENT_BUY_IN`) | 100 |
| Fichas de juego por inscrito (no son PoleCoins) | `events.initial_chips` (por defecto `EVENT_INITIAL_CHIPS`) | 1000 |
| Comisión de la casa sobre el botín | `events.commission_pct` (por defecto `EVENT_COMMISSION_PCT`) | 10 % |
| Inscritos mínimos para que haya torneo | configuración `EVENT_MIN_PLAYERS` | 4 |
| Fichas que hay que apostar para cobrar del botín (HU2-12) | configuración `EVENT_MIN_WAGER_PCT` | 50 % |

- Una sola inscripción por jugador y evento; los reintentos no cobran doble.
- Solo se inscribe con el torneo `abierto` (antes de la largada). La largada la marca el Motor con el **tiempo de carrera**.
- Al largar con menos de `EVENT_MIN_PLAYERS` inscritos: se devuelve el `buy_in` a todos (`refund` en el ledger), el botín queda en 0 y el torneo pasa a `cancelado`. La carrera sigue y las estáticas se liquidan igual.
- El rol (`player`) lo valida el **Orquestador** con `X-User-Rol`. Pagos no consulta usuarios.

## Estados del evento

| Momento | `status` (carrera) | `tournament_status` (torneo) |
| --- | --- | --- |
| Creado, inscripciones abiertas | `programado` | `abierto` |
| Largó con suficientes inscritos | `en_curso` | `activo` |
| Largó con pocos inscritos | `en_curso` | `cancelado` |
| Terminó y se repartió el botín (HU2-12) | `finalizado` | `repartido` |

## API interna de Pagos

Todas exigen `X-Internal-Service-Token`.

| Método y ruta | Quién | Respuestas |
| --- | --- | --- |
| `POST /internal/events` `{event_id (uuid), race_key, starts_at, buy_in?, initial_chips?, commission_pct?}` | Quien arranque la simulación (HU2-07, Tomás: puede usarla o reemplazarla) | 201 creado · 200 ya existía igual · 409 mismo id con otra configuración |
| `GET /internal/events/{id}` | Todos | 200 `{status, tournament_status, buy_in, pot, registered, starts_at, …}` · 404 |
| `POST /internal/events/{id}/registrations` `{user_id}` + `Idempotency-Key: event-entry:{id}:{user_id}` | Orquestador | 201 inscrito `{buy_in, initial_chips, pot, saldo}` · 200 ya inscrito (no cobra) · 400 clave inválida · 404 evento · 409 inscripciones cerradas · 422 saldo insuficiente |
| `GET /internal/events/{id}/registrations/{user_id}` | Orquestador | 200 `{tournament_status, refunded}` · 404 no inscrito |
| `POST /internal/events/{id}/start` | Motor | 200 `{status, tournament_status, pot, players, refunded, nueva}`; repetirla no devuelve dos veces |

## Datos (PostgreSQL de Pagos)

Las tablas las definió T2-03 (Juan Diego, `app/models.py`); esta HU solo las usa:

- `events`: id (uuid), race_key, status, tournament_status, starts_at (largada programada), ended_at, buy_in, initial_chips, commission_pct, pot.
- `event_entries`: **PK (event_id, user_id)**; chips_final, chips_wagered, position_final, prize y eligible quedan vacías hasta HU2-12.
- `wallets`: el saldo de PoleCoins que se descuenta y se devuelve.
- `ledger_entries`: movimientos `buy_in` y `refund`, con `event_id`.

Concurrencia: inscribir y largar bloquean la fila del evento (`SELECT … FOR UPDATE`), así nunca se cruzan y el botín no pierde sumas.

## Notas para el equipo

- **Juan Diego (T2-03):** uso tus tablas sin cambiarlas; el saldo sale de `wallets`. `min_players` y `min_wager_pct` quedaron en configuración porque no están en el modelo; si se agregan al documento, se pasan a columnas de `events`.
- **Tomás (HU2-07):** `POST /internal/events` ya existe; úsala o reemplázala al crear el evento automáticamente.
- **Guillermo (HU2-11/12):** las fichas viven en Redis; `players` de la largada dice a quién crearle `initial_chips`. Para el reparto: `events.pot`, `events.commission_pct`, `EVENT_MIN_WAGER_PCT` y las columnas de `event_entries`.
- Cambian tablas y restricciones: hay que recrear la base con `docker compose down -v`.
