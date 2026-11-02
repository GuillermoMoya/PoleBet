# HU2-12 — Reparto del botín

## Resumen

Al terminar la carrera, el botín del torneo (las inscripciones cobradas) menos
la comisión de la casa se reparte entre los tres primeros puestos elegibles.
Los premios son tokens. Las fichas finales, el puesto y el premio de cada
inscrito quedan en `event_entries` y el estado del torneo se borra de Redis.

```
fin de carrera ─▶ Motor lee fichas finales (Redis, HU2-11)
                  └─ POST /internal/events/{id}/close ─▶ Pagos: comisión, 50/30/20,
                                                          empates, residuo, premios,
                                                          event_entries, ledger
                  ◀─ 201 reparto (o 200 si ya estaba)
                  └─ borra torneo:{evento}:* en Redis
```

## Qué cubre

| ID | Requisito |
| --- | --- |
| RF-16 | Al terminar la carrera se reparte el botín; fichas finales, puesto y premio quedan en `event_entries` y Redis se limpia |
| RNF-11 | En cada botín, premios + comisión + residuo suman exactamente lo cobrado en inscripciones |
| RNF-12 | Repetir el cierre no paga doble |

Componentes: Motor (Go, Redis) y Pagos (Python, PostgreSQL Pagos).

## Reglas (del documento de requisitos)

| Regla | Valor | Dónde vive |
| --- | --- | --- |
| Comisión de la casa sobre el botín | 10 % | `events.commission_pct` |
| Reparto entre los 3 primeros elegibles | 50 / 30 / 20 % | `PORCENTAJES_PODIO` en `app/services/reparto.py` |
| Mínimo apostado para ser elegible | 50 % de las fichas iniciales | `EVENT_MIN_WAGER_PCT` (configuración, la agregó HU2-06) |
| Empates | el grupo comparte puesto y divide su %; el siguiente toma el puesto que sigue | `calcular_reparto` |
| Premios | tokens enteros; el residuo va a la casa | `calcular_reparto` |

Ejemplo del enunciado: tres empatan en el 1.º. Se reparten el 50 % en tres
partes iguales, y el siguiente jugador es el 2.º y cobra el 30 %. Nadie llega al
3.º, así que su 20 % va a la casa.

## Cómo se calcula

`calcular_reparto` (función pura, sin base de datos):

1. **Comisión** = piso(`pot × commission_pct / 100`). **Repartible** = `pot − comisión`.
2. **Mínimo apostado** = techo(`initial_chips × EVENT_MIN_WAGER_PCT / 100`), igual
   que la barra del ranking en el Motor. Con 0 % todos son elegibles.
3. Se ordena a los inscritos por fichas finales (mayor primero).
   `position_final` es el puesto denso entre **todos** los inscritos: es el que
   el jugador vio en el ranking.
4. Entre los **elegibles** se calculan los puestos de reparto (también densos).
   Al puesto `p` (1, 2 o 3) con `n` jugadores le toca, a cada uno,
   `piso(repartible × %p / (100 × n))`. Del 4.º puesto en adelante, 0.
5. **Residuo** = `pot − comisión − Σ premios`. Junta los truncamientos y los
   porcentajes sin jugadores. Nunca es negativo, porque cada puesto paga como
   máximo su porcentaje y 50 + 30 + 20 = 100.

Ejemplo con botín 400 (4 inscritos × 100) y comisión 10 %:

| Jugador | Fichas | Apostadas | Elegible | Puesto | Puesto de reparto | Premio |
| --- | --- | --- | --- | --- | --- | --- |
| D | 4000 | 100 | no | 1 | — | 0 |
| A | 3000 | 900 | sí | 2 | 1 | 180 |
| B | 2000 | 600 | sí | 3 | 2 | 108 |
| C | 1000 | 500 | sí | 4 | 3 | 72 |

Comisión 40, premios 360, residuo 0: 360 + 40 + 0 = 400.

## API

**Pagos** (`services/pagos`): `POST /internal/events/{event_id}/close`

```json
{ "players": [ { "user_id": "…", "chips_final": 3000, "chips_wagered": 900 } ] }
```

Header `Idempotency-Key: event-close:{event_id}` y token de servicio.

| Respuesta | Cuándo |
| --- | --- |
| 201 | reparto hecho: `{event_id, status, tournament_status, pot, commission, prizes_total, residue, players[], nueva: true}` |
| 200 | ya estaba repartido: devuelve lo guardado sin pagar. Acepta `players` iguales a lo guardado o una lista vacía |
| 400 | `Idempotency-Key` distinta de `event-close:{event_id}` |
| 404 | evento inexistente, o un ganador sin billetera (no se paga a nadie) |
| 409 | evento sin largar; jugadores que no son exactamente los inscritos; ya repartido con otras fichas |

Si el torneo se canceló en la largada (HU2-06 ya devolvió las inscripciones), se
llama con `players: []` y solo se finaliza la carrera.

**Motor** (`services/motor/internal/fichas`):

| Operación | Qué hace |
| --- | --- |
| `Almacen.CerrarTorneo(evento, pagos)` | lee de Redis las fichas y lo apostado de cada inscrito, llama a Pagos y, si confirma, borra las claves del torneo |
| `PagosCierreHTTP.CerrarEvento` | cliente REST del endpoint; verifica que `prizes_total + commission + residue == pot` antes de aceptar la respuesta |

## Persistencia

En una sola transacción de PostgreSQL Pagos:

- `event_entries`: `chips_final`, `chips_wagered`, `position_final`, `eligible`, `prize`.
- `wallets`: se acredita cada premio mayor que 0.
- `ledger_entries`: un movimiento `prize` por ganador, con clave
  `event-prize:{evento}:{usuario}`.
- `events`: `status = finalizado`, `ended_at`, `tournament_status = repartido`.
  `pot` no se toca: queda como registro de lo cobrado.

La comisión y el residuo no se escriben en el ledger: `ledger_entries.user_id`
es obligatorio y la casa no tiene billetera. Se obtienen de `events.pot` y de la
suma de los `prize`. HU2-20 (resumen financiero) puede calcularlos así.

## Decisiones

1. **El cálculo vive en Pagos (opción A).** Pagos es dueño del botín
   (`events.pot`), la comisión y los tokens. Calcular y pagar en la misma
   transacción garantiza RNF-11 y RNF-12 sin coordinar dos servicios. El Motor
   solo aporta lo que es suyo: las fichas de Redis.
2. **Puesto denso, también en el ranking.** El enunciado dice que, tras un
   empate de tres en el 1.º, el siguiente es el 2.º. Al revisar esto se corrigió
   HU2-11, que usaba puesto con salto (1,1,1,4). Commit
   `fix(hu2-11): puesto denso…` en la rama de HU2-11.
3. **Dos puestos distintos.** `position_final` es el puesto entre todos (el que
   vio el jugador en el ranking). El reparto usa el puesto entre elegibles: un
   jugador con muchas fichas que no apostó el mínimo no ocupa un lugar del podio.
4. **Truncar cada premio.** Los premios son enteros y el sobrante va a la casa,
   como dice el enunciado. Así el residuo nunca es negativo y no hace falta
   decidir quién recibe un token extra.
5. **Idempotencia por estado del evento + bloqueo de fila.** El primer cierre
   deja `tournament_status = repartido`; los siguientes leen eso y devuelven lo
   guardado. Cierres simultáneos se serializan con `SELECT … FOR UPDATE`. La
   clave única de cada `prize` en el ledger es una segunda protección.
6. **Redis se limpia después de que Pagos confirma.** Si se limpiara antes y
   Pagos fallara, las fichas se perderían. En el orden elegido, el peor caso es
   que Redis quede sin limpiar; repetir el cierre lo arregla (y las claves
   expiran a las 24 h).
7. **Archivos propios en Pagos.** `routers/reparto.py`, `schemas/reparto.py` y
   `services/reparto.py` comparten el prefijo `/internal/events` con HU2-06 pero
   no editan sus archivos. El único archivo compartido es `routers/__init__.py`
   (una línea).
8. **Rama apilada sobre HU2-06 y HU2-11.** El cierre usa la largada, las
   inscripciones y `EVENT_MIN_WAGER_PCT` de HU2-06 (PR #26, aún sin mergear) y
   las fichas de HU2-11. La rama `feature/hu2-12-reparto-botin` las incluye por
   merge. Su PR mostrará esos commits hasta que los PR de HU2-06 y HU2-11 se
   mergeen; después solo quedará lo de esta historia.

## Pruebas ejecutadas

**Pagos**, con `python:3.12-slim` y un `postgres:17-alpine` desechable:
`pytest` → **65 pasan** (16 nuevas en `tests/test_reparto.py`).

- Cálculo puro: reparto 50/30/20 sin residuo; el ejemplo del enunciado (tres
  empatados en el 1.º, el siguiente es 2.º, el 20 % va a la casa); empate en el
  2.º; residuo por redondeo; no elegibles fuera del podio; sin elegibles; con
  mínimo 0 todos elegibles; redondeo del mínimo y la comisión; el orden de
  entrada no cambia el resultado; e invariante `premios + comisión + residuo =
  botín` sobre 500 casos aleatorios reproducibles.
- Con PostgreSQL: cierre que paga y llena `event_entries` y el ledger; repetir
  el cierre (con las mismas fichas o vacío) no paga doble y con otras fichas da
  409; 4 cierres simultáneos pagan una sola vez; jugadores faltantes, extra o
  repetidos → 409; evento sin largar → 409; clave inválida → 400; sin token →
  401; torneo cancelado solo finaliza la carrera.

**Motor**, con `golang:1.25-alpine`: `gofmt`, `go build`, `go vet` y
`go test ./...` pasan. Nuevas en `internal/fichas/cierre_test.go`: el cierre
envía fichas finales correctas y limpia Redis; si Pagos falla, Redis queda
intacto y el reintento funciona; el cliente HTTP envía la clave, el token y la
ruta correctos; rechaza una respuesta que no cuadra con el botín; traduce el 409.

No se probó el flujo completo con Docker Compose (Motor + Redis + Pagos reales):
queda para la integración conjunta.

## Límites conocidos

- **El disparo del cierre no existe todavía.** Detectar el fin de la carrera y
  que las estáticas ya se liquidaron es de HU2-07. Esta HU deja
  `Almacen.CerrarTorneo` listo para que ese flujo lo llame. Mientras tanto se
  prueba llamando al endpoint de Pagos.
- **Apuestas de torneo pendientes al cerrar.** Si al cerrar queda una apuesta de
  torneo sin liquidar, sus fichas cuentan como apostadas y perdidas. Por eso el
  cierre debe llamarse después de liquidar todos los mercados (RF-16).
- `EVENT_MIN_WAGER_PCT` vive en configuración (decisión de HU2-06) y se lee al
  cerrar: si cambia entre la largada y el cierre, se usa el valor nuevo.
- La comisión y el residuo no tienen fila propia en el ledger (ver Persistencia).
- Si un ganador no tiene billetera, el cierre falla completo (404) y no se paga
  a nadie; se puede repetir cuando la billetera exista.

## Requisitos de referencia

Al terminar la carrera, repartir el botín menos la comisión del 10 % en 50/30/20 %
entre los 3 primeros puestos elegibles; los empatados comparten puesto y dividen
su porcentaje; los premios son tokens enteros y el residuo va a la casa; premios
+ comisión + residuo suman el botín; repetir el cierre no paga doble; y al
terminar, fichas finales, puesto y premio quedan en `event_entries` y Redis se
limpia.
