# HU2-21 — Rentabilidad por tipo y plantilla

## Resumen

El administrador quiere saber qué apuestas dejan ganancia y cuáles pérdida, para
ajustar el catálogo y los multiplicadores. Esta historia calcula, para las
apuestas estáticas y dinámicas y, dentro de cada tipo, por plantilla: cuántas
apuestas hubo, el porcentaje de acierto, lo apostado, lo pagado, la ganancia
neta de la plataforma y el multiplicador promedio; además, el ranking de las N
plantillas con más y con menos ganancia y cuántas veces se generó cada una.

La consulta la responde **solo Pagos**, porque ahí viven los eventos, los
mercados (con su `kind` y su `template`), las apuestas y el ledger. El
administrador la pide por el **Broker (RabbitMQ)**, de forma asíncrona, para no
competir con el juego en vivo.

## Qué cubre

| ID | Requisito |
| --- | --- |
| RF-26 | Rentabilidad por tipo (estática/dinámica) y por plantilla: cantidad, % de acierto, apostado, ganancia neta, multiplicador promedio, y ranking de las N plantillas con más/menos ganancia y cuántas veces se generó cada una |
| RF-30 (parte Pagos) | El consumidor toma las consultas de a una (prefetch 1) y responde por una cola de respuesta con `correlation_id` |
| RF-29 (parcial) | La consulta acepta filtros por evento, rango de fechas, tipo y plantilla |

Componente: Pagos (consumidor AMQP + consulta SQL). El publicador (Admin) y la
CLI son de otras historias (ver fronteras).

## Fronteras con el equipo

El flujo completo de una consulta del administrador es:

```
CLI (William) ─▶ Admin (Iván, HU2-18) ─[cola admin.consultas]─▶ consumidor Pagos (esta HU)
                                                                        │
CLI ◀── Admin ◀─[cola reply_to, correlation_id]────────────── respuesta ┘
```

De esa cadena, **esta HU implementa solo el consumidor de Pagos**: tomar la
consulta, calcular la rentabilidad y responder. El publicador en Admin (HU2-18)
y la CLI (HU2-17) son de Iván y William.

Como HU2-18 todavía no existe en el repositorio (no hay código AMQP en ninguna
rama), **esta HU define el contrato del Broker** (nombres de colas y formato de
los mensajes) y lo deja documentado aquí y en `app/schemas/consultas.py`. Si
Iván elige otro formato, se ajusta solo en `app/consumer.py` y
`app/schemas/consultas.py`; la consulta SQL no cambia.

Decisiones de frontera acordadas con el equipo:

1. Esta HU **no toca `services/admin`** (territorio de Iván); solo el consumidor
   de Pagos.
2. Se añade la dependencia **`pika`** a `services/pagos/requirements.txt`.
3. Se reemplaza el `command` placeholder del servicio `pagos-consumer` en
   `docker-compose.yml` por `python -m app.consumer` (una línea, en la sección
   ya reservada para esto).

## Contrato del Broker (lo define esta HU)

| Elemento | Valor |
| --- | --- |
| Cola de consultas | `admin.consultas` (durable). Admin publica aquí |
| Entrega | `prefetch = 1`: una consulta a la vez (RF-30) |
| Respuesta | Pagos publica en la cola `reply_to` del mensaje, con el mismo `correlation_id` |

`correlation_id` y `reply_to` viajan en las **propiedades** del mensaje AMQP
(patrón petición-respuesta estándar de RabbitMQ), no en el cuerpo.

Cuerpo de la consulta (JSON):

```json
{ "tipo": "rentabilidad",
  "filtros": { "event_id": "…", "desde": "…", "hasta": "…",
               "kind": "estatica", "template": "ganador", "top_n": 5 } }
```

Cuerpo de la respuesta (JSON):

```json
{ "tipo": "rentabilidad", "ok": true,
  "datos": { "filas": [ … ], "top_ganancia": [ … ], "peor_ganancia": [ … ],
             "total_apuestas": 3, "total_apostado": 250, "total_pagado": 200,
             "ganancia_neta": 50 } }
```

Ante un error, la respuesta es `{"tipo":"rentabilidad","ok":false,"error":"…"}`
(`json_invalido`, `tipo_no_soportado`, `filtros_invalidos`, `kind_invalido`,
`error_interno`). El consumidor **nunca deja de responder**: así la CLI no se
cuelga esperando y el timeout de [10 s] lo decide Admin, no Pagos.

## Cómo se calcula (RF-26)

Sobre `bets` unida a `markets` por `market_id`, agrupando por `markets.kind` y
`markets.template`:

| Métrica | Cálculo |
| --- | --- |
| apuestas | cuántas apuestas resueltas (won/lost; las `pending` no cuentan) |
| aciertos / % acierto | `won` sobre el total del grupo |
| apostado | `SUM(amount)` |
| pagado | `SUM(payout)` de las ganadoras |
| ganancia_neta | `apostado − pagado` (lo que gana o pierde la casa) |
| multiplicador_promedio | `AVG(multiplier)` |
| mercados_generados | cuántos mercados se crearon con esa plantilla |

El ranking `top_ganancia` / `peor_ganancia` ordena por `ganancia_neta` las
plantillas que tuvieron apuestas (una plantilla sin apuestas no informa sobre
rentabilidad, aunque sí aparece en el detalle con ceros y su cuenta de mercados).

### Alcance de la ganancia: solo tokens (individual)

La "ganancia de la plataforma" es un concepto de **tokens**. Las apuestas de
torneo se pagan en **fichas**, que no son dinero y no pasan por el ledger. Por
eso las métricas de dinero (apostado, pagado, ganancia) se calculan sobre las
apuestas con `mode = individual`. Las de torneo se excluyen de la ganancia; la
participación del torneo la cubre HU2-22 (otra consulta). Esto es coherente con
que RF-26 habla de qué apuestas le dejan ganancia o pérdida a la casa.

## Archivos

| Archivo | Qué es |
| --- | --- |
| `app/services/rentabilidad.py` | la consulta SQL (`calcular_rentabilidad`, filtros, agregación) |
| `app/services/consulta_handler.py` | `procesar_consulta(db, bytes) -> bytes`: valida el mensaje y arma la respuesta, sin RabbitMQ |
| `app/consumer.py` | proceso AMQP (`python -m app.consumer`): cola, prefetch 1, reply_to + correlation_id, reintentos de conexión |
| `app/schemas/consultas.py` | contrato del Broker (Pydantic) |
| `tests/test_rentabilidad.py` | la agregación y el handler contra PostgreSQL |
| `tests/test_consumer.py` | el callback AMQP con un canal falso (reply_to, correlation_id, ack) |

El consumidor hace **ack siempre** tras intentar responder: una consulta no tiene
efectos secundarios, así que no se reencola; una consulta malformada no bloquea
la cola. La conexión al Broker se reintenta para no depender del orden de
arranque de los contenedores.

## Decisiones

1. **Solo el consumidor de Pagos.** El publicador (Admin) y la CLI son de HU2-18
   y HU2-17. Esta HU entrega su parte y define el contrato de colas para
   integrar cuando exista HU2-18.
2. **Contrato del Broker definido aquí.** No había código AMQP en el repo;
   definirlo desde Pagos desbloquea el trabajo y queda documentado para Iván.
3. **`pika` (AMQP síncrono).** El consumidor es un proceso aparte y bloqueante
   (patrón consumidor), para el que `pika` encaja mejor que una librería async.
4. **Ganancia solo en tokens (individual).** Ver "Alcance de la ganancia".
5. **El consumidor nunca se cae ni deja de responder.** Todo error se traduce a
   una respuesta `ok:false`; así la CLI siempre recibe algo dentro de su ventana.
6. **Lógica separada del transporte.** `procesar_consulta` no sabe de RabbitMQ,
   lo que permite probarla a fondo sin un broker.

## Pruebas ejecutadas

En `services/pagos`, con `python:3.12-slim` y un `postgres:17-alpine` desechable:
`pytest` → **49 pasan** (las nuevas de esta HU incluidas).

- `test_rentabilidad.py`: agregación por tipo y plantilla con ganancia neta;
  las apuestas de torneo no cuentan en la ganancia; las `pending` y los mercados
  sin apuestas salen con ceros; filtros por kind, fecha y evento; `kind`
  inválido; y el handler (`procesar_consulta`) en el camino feliz y en todos los
  casos de error (devuelve `ok:false` sin lanzar).
- `test_consumer.py`: el callback responde por `reply_to` con el mismo
  `correlation_id` y hace `ack`; sin `reply_to` no publica pero igual hace `ack`;
  y hace `ack` aunque el procesamiento falle (no bloquea la cola).

Verificación adicional: el consumidor importa y declara la cola correctamente
con `pika` instalado (`import app.consumer` OK, cola `admin.consultas`).

**No ejecutado:** la prueba end-to-end contra un RabbitMQ real (publicar y
recibir la respuesta) no se pudo correr en este entorno: la imagen
`rabbitmq:3.13-management-alpine` falló al arrancar por un problema de permisos
del `.erlang.cookie` en Docker Desktop (Windows), ajeno a esta HU. Queda para la
integración conjunta con Docker Compose, donde RabbitMQ ya corre.

## Límites conocidos

- El flujo completo (CLI → Admin → Broker → Pagos → Admin → CLI) necesita HU2-18
  (Admin, Iván) y HU2-17 (CLI, William). Esta HU entrega y prueba el extremo de
  Pagos contra el contrato de colas documentado.
- La ganancia se mide sobre apuestas individuales resueltas; las `pending` no
  entran (todavía no tienen resultado).
- El `top_n` del ranking tiene tope 100 para no devolver respuestas enormes.
- Si Admin publica con otro formato de mensaje, se ajusta `app/consumer.py` y
  `app/schemas/consultas.py`; la consulta SQL no cambia.

## Requisitos de referencia

Calcular, para estáticas y dinámicas y por plantilla, la cantidad de apuestas,
el porcentaje de acierto, lo apostado, la ganancia neta y el multiplicador
promedio, con el ranking de las plantillas más y menos rentables y cuántas veces
se generó cada una; responder esa consulta desde Pagos por el Broker, de a una
(prefetch 1) y por una cola de respuesta con `correlation_id`, aceptando filtros
por evento, fechas, tipo y plantilla.
