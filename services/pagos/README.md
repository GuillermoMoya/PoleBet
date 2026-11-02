# Pagos (FastAPI + PostgreSQL Pagos)

Servicio dueño de PostgreSQL Pagos: billeteras, eventos, inscripciones,
mercados, apuestas y ledger. Desde el prototipo 2 no recibe tráfico del
navegador: lo llaman el Orquestador y el Motor por las rutas `/internal/*`.
Las cuentas, el login y el JWT viven en Usuarios.

Modelo de datos: `documentation/modelo-datos-p2.pdf` (el del prototipo 1 queda
en `documentation/modelo-datos-pagos-p1.pdf`).

## Estructura

```
app/
├── main.py               # crea la app: lifespan (espera BD + crea tablas), routers
├── core/config.py        # variables de entorno (Settings)
├── core/internal_auth.py # comparación del token de servicio
├── db.py                 # engine, SessionLocal, get_db, wait_for_db, init_db
├── deps.py               # DbSession, ServicioInterno
├── models.py             # wallets, events, event_entries, markets, bets, ledger_entries
├── schemas/              # modelos Pydantic de entrada/salida
├── routers/              # endpoints REST (registrar cada router en routers/__init__.py)
└── services/             # lógica de negocio (billetera, débito, liquidación)
tests/                    # pytest
```

## Cómo agregar una HU

1. Esquemas en `app/schemas/<tema>.py`.
2. Lógica en `app/services/<tema>.py` (recibe la `Session`).
3. Endpoints en `app/routers/<tema>.py` con
   `router = APIRouter(prefix="/internal/<tema>", dependencies=[ServicioInterno])`.
4. Agregar el router a `ROUTERS` en `app/routers/__init__.py`.

Todas las rutas `/internal/*` exigen el header `X-Internal-Service-Token`,
igual a `INTERNAL_API_TOKEN` (mínimo 32 caracteres; sin él el servicio no
arranca). Sin token o con uno distinto: 401. El rol del jugador no se valida
aquí: lo valida el Orquestador con `X-User-Rol`.

## Billetera (T2-03, HU2-04)

| Endpoint | Qué hace | Respuestas |
| --- | --- | --- |
| `POST /internal/wallets` | Body `{"user_id"}` + `Idempotency-Key: wallet:{user_id}`. Crea la billetera con saldo 0 (registro, HU2-01) | 201 `{user_id, balance, cop_por_token}` · 200 ya existía (no la toca) · 400 clave distinta de `wallet:{user_id}` |
| `GET /internal/wallets/{user_id}` | Saldo y tasa | 200 `{user_id, balance, cop_por_token}` · 404 sin billetera |
| `POST /internal/purchases` | Body `{"user_id", "monto_cop"}` + `Idempotency-Key: purchase:{user_id}:{clave}`. Pago simulado (siempre aprobado), convierte con `COP_PER_TOKEN` y acredita (HU2-04) | 201 `{compra_id, tokens_acreditados, saldo, creada_en}` · 200 reenvío de la misma clave (no acredita de nuevo) · 400 clave que no es `purchase:{user_id}:…` · 404 sin billetera · 409 misma clave con otro monto · 422 monto ≤ 0, no múltiplo de la tasa o mayor a $10.000.000 |

- La llave primaria `wallets.user_id` hace la creación idempotente, aunque
  lleguen dos peticiones a la vez (`INSERT … ON CONFLICT DO NOTHING`).
- `user_id` no es llave foránea: `users` está en otra base.
- La compra guarda la clave tal cual en `ledger_entries.idempotency_key`
  (única). Lleva el `user_id` para que dos jugadores con la misma clave no
  choquen; la arma el Orquestador.
- Ledger y saldo en una sola transacción; el saldo se suma con
  `balance = balance + x` en un solo `UPDATE`.

## Apuestas individuales (Motor → Pagos)

| Endpoint | Entrada | Respuestas |
| --- | --- | --- |
| `POST /internal/debit` | `{bet_id, user_id, market_id, option, amount, multiplier, lap, idempotency_key}` + `Idempotency-Key: bet-debit:{bet_id}` | 201 `{bet_id, event_id, status, saldo, nueva}` · 200 reenvío idéntico · 400 clave inválida · 404 billetera o mercado inexistente · 409 contenido distinto para el ID/clave, o ya apostó en ese mercado · 422 saldo insuficiente |
| `POST /internal/settlements` | `{bet_id, result: "won"\|"lost", idempotency_key}` + `Idempotency-Key: bet-settlement:{bet_id}` | 201 `{bet_id, status, payout, saldo, nueva}` · 200 reenvío idéntico · 400 clave inválida · 404 apuesta inexistente · 409 ya liquidada con otro resultado · 422 apuesta de torneo |

- El débito descuenta de `wallets`, crea la apuesta (`mode = individual`,
  `event_id` del mercado) y escribe `debit` en el ledger en una transacción
  (RNF-11). El `UPDATE` atómico `balance >= monto` impide que dos apuestas
  simultáneas gasten el mismo saldo.
- `market_id` es un `markets.id`: el mercado tiene que existir en Pagos.
- Una sola apuesta por usuario y mercado: restricción única `(user_id, market_id)`.
- La liquidación paga monto × multiplicador si acertó (incluye lo apostado,
  RF-14), truncado a tokens enteros, y escribe `settle`. Si falló escribe
  `settle` con 0 tokens: la clave única impide liquidar dos veces.
- Las apuestas de torneo no pasan por aquí: se pagan en fichas (Redis).

## Consultas del administrador por el Broker (HU2-21)

Pagos corre un **consumidor AMQP aparte** (contenedor `pagos-consumer`,
`python -m app.consumer`) que responde las consultas de rentabilidad del
administrador. No es una ruta HTTP: llega por RabbitMQ.

| Elemento | Valor |
| --- | --- |
| Cola de consultas | `admin.consultas` (durable), `prefetch = 1` (RF-30) |
| Respuesta | a la cola `reply_to` del mensaje, con el mismo `correlation_id` |
| Consulta | `{"tipo":"rentabilidad","filtros":{event_id?,desde?,hasta?,kind?,template?,top_n?}}` |
| Respuesta | `{"tipo":"rentabilidad","ok":true,"datos":{…}}` o `{"ok":false,"error":"…"}` |

Calcula, por `kind` (estatica/dinamica) y `template`: apuestas, % de acierto,
apostado, pagado, ganancia neta, multiplicador promedio y mercados generados, más
el ranking de plantillas con más y menos ganancia. La ganancia se mide sobre las
apuestas **individuales** (tokens); las de torneo mueven fichas y no cuentan.

Lo publica Admin (HU2-18, Iván) cuando exista; el contrato de colas lo define
esta HU. Lógica en `app/services/rentabilidad.py` y `app/consumer.py`. Detalle en
`documentation/hu2-21-rentabilidad.md`.

## Cierre del evento y reparto del botín (HU2-12, Motor → Pagos)

| Endpoint | Entrada | Respuestas |
| --- | --- | --- |
| `POST /internal/events/{event_id}/close` | `{"players": [{user_id, chips_final, chips_wagered}]}` + `Idempotency-Key: event-close:{event_id}` | 201 `{event_id, status, tournament_status, pot, commission, prizes_total, residue, players[], nueva}` · 200 ya estaba cerrado (devuelve lo guardado, no paga) · 400 clave inválida · 404 evento inexistente o ganador sin billetera · 409 evento sin largar, jugadores que no coinciden con los inscritos o reparto previo con otras fichas |

- El Motor manda las fichas finales de **todos** los inscritos (viven en
  Redis, HU2-11). Pagos calcula y paga en una sola transacción, con la fila
  del evento bloqueada (`FOR UPDATE`).
- Comisión = piso(`pot × commission_pct / 100`). El resto se reparte
  50 / 30 / 20 % entre los 3 primeros puestos **elegibles** (apostaron al menos
  `EVENT_MIN_WAGER_PCT` de las fichas iniciales, redondeado hacia arriba).
- Empates: el grupo ocupa un solo puesto y divide su porcentaje; el siguiente
  grupo toma el puesto que sigue (1,1,2). Cada premio se trunca a tokens
  enteros; truncamientos y puestos sin elegibles son el `residue` (casa).
  Siempre `prizes_total + commission + residue == pot` (RNF-11).
- Escribe en `event_entries` `chips_final`, `chips_wagered`, `position_final`
  (puesto entre todos los inscritos), `eligible` y `prize`; acredita `wallets`
  y anota `prize` en el ledger (`event-prize:{evento}:{usuario}`). Deja
  `status = finalizado` y `tournament_status = repartido`.
- Torneo cancelado en la largada: con `players: []` solo finaliza la carrera.
- Lógica en `app/services/reparto.py` (`calcular_reparto` es una función pura).
  Detalle en `documentation/hu2-12-reparto-botin.md`.

## Correr

Con todo el sistema: `docker compose up --build` desde la raíz. Pagos no
publica puerto; se prueba desde otro contenedor de la red `interna` o con las
pruebas.

Pruebas (Python 3.12+). El PostgreSQL del compose no publica su puerto, así
que se usa uno aparte:

```bash
docker run -d --name pg-pruebas -e POSTGRES_USER=arqui -e POSTGRES_PASSWORD=arqui_pass \
    -e POSTGRES_DB=pagos_db -p 55432:5432 postgres:17-alpine
cd services/pagos
pip install -r requirements-dev.txt
DATABASE_URL=postgresql+psycopg://arqui:arqui_pass@localhost:55432/pagos_db pytest
```

Sin PostgreSQL accesible, las pruebas que tocan la BD se saltan.

> **`create_all` no modifica tablas existentes.** Si tu volumen tiene tablas de
> una versión anterior del modelo, recréalo con `docker compose down -v`. Para
> traer las cuentas y saldos del prototipo 1, ver `scripts/migrar-p1.sh`.
