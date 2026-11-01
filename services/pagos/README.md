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

## Billetera (T2-03)

| Endpoint | Qué hace | Respuestas |
| --- | --- | --- |
| `POST /internal/wallets` | Body `{"user_id"}` + `Idempotency-Key: wallet:{user_id}`. Crea la billetera con saldo 0 (registro, HU2-01) | 201 `{user_id, balance, cop_por_token}` · 200 ya existía (no la toca) · 400 clave distinta de `wallet:{user_id}` |
| `GET /internal/wallets/{user_id}` | Saldo y tasa | 200 `{user_id, balance, cop_por_token}` · 404 sin billetera |

- La llave primaria `wallets.user_id` hace la creación idempotente, aunque
  lleguen dos peticiones a la vez (`INSERT … ON CONFLICT DO NOTHING`).
- `user_id` no es llave foránea: `users` está en otra base.
- La lógica de compra (`comprar_tokens` en `app/services/billetera.py`) ya
  acredita en `wallets`; su endpoint `POST /internal/purchases` es HU2-04.

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
