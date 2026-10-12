# HU-07 — Liquidación de apuestas

## Resumen

Cuando termina la ventana de observación de un mercado, el Motor decide la
opción ganadora, marca cada apuesta como ganada o perdida y le pide a Pagos
que sume los puntos. Todo con el reloj del servidor, con reintentos y sin
duplicar efectos.

```
abre_en ──(30 s apuestas)──▶ cierra_en ──(60 s observación)──▶ observa_hasta ──▶ liquidación
         HU-11 abre               HU-11 cierra                    HU-07: el worker lo liquida
```

- El **cierre** (`abierto → cerrado`) es de HU-11 (paquete `mercados`), con un
  temporizador. HU-07 no cierra nada.
- El **worker de HU-07** (`liquidacion.Worker`) consulta cada 2 s los mercados
  ya `cerrado` cuyo `observa_hasta` pasó, y los liquida. Puede tardar hasta un
  intervalo de polling (2 s) después de `observa_hasta`.

## El mercado: Sí/No sobre la carrera

En el prototipo hay **un solo tipo de mercado**, `cambio_posicion`:

> ¿Habrá algún cambio de posición durante la ventana de observación?
> Opciones: **Sí** / **No**

| Resultado | Condición |
| --------- | --------- |
| **Sí** | La reproducción emitió al menos un evento de `posicion` de la carrera del mercado con `emitido_en` en `[cierra_en, observa_hasta]` (límites incluidos). |
| **No** | No hubo ninguno **y** la reproducción estuvo viva durante toda la ventana: su `iniciada_en ≤ cierra_en` y su último `latido_en ≥ observa_hasta`. |
| Pendiente | No hubo eventos y no hay constancia de que la reproducción corriera (nadie la lanzó, se cayó o se reinició en medio). El mercado queda `cerrado` y reintentable, con el motivo en `liquidacion_error`. |

Cada fila de `/position` de OpenF1 es un cambio de posición de un piloto, así
que "se emitió un evento de posición" equivale a "hubo un cambio de posición".

Las apuestas se marcan ganadas o perdidas. Una ganadora suma
`10 + (monto apostado × 0,5)` puntos; una perdedora suma cero. Los tokens
apostados no se devuelven en ninguno de los dos resultados. La no devolución
al ganador es el supuesto actual del prototipo (el documento de requisitos
lo presenta como supuesto abierto), no una regla universal confirmada.

### Cómo encaja con el resto

- **POST /mercados (HU-11):** el contrato no cambia (`{pregunta, opciones}`).
  El motor fija `tipo: "cambio_posicion"` y responde **400** si las opciones no
  son exactamente `["Sí", "No"]`, en ese orden. El texto de la pregunta no se
  valida contra la regla: la liquidación se guía **solo por el tipo**.
- **Front (/admin):** la pregunta y las opciones vienen prellenadas y en solo
  lectura (`src/mercados/mercado.ts`, `PREGUNTA_CAMBIO_POSICION` y
  `OPCIONES_SI_NO`).
- **HU-06 (apostar):** no cambia. Acepta la opción si está en
  `mercado.opciones`, así que el jugador apuesta "Sí" o "No".
- **Pagos:** no cambia. `bets.option` guarda "Sí"/"No" (cabe en `VARCHAR(100)`).
- **Fuente de verdad:** las constantes viven en
  `services/motor/internal/mercados/servicio.go` (`TipoCambioPosicion`,
  `OpcionSi`, `OpcionNo`, `PreguntaCambioPosicion`); `liquidacion` las importa
  de ahí. El validador de Mongo (`db/mongo/init/01-init-collections.js`) solo
  admite `tipo: 'cambio_posicion'`.

## La reproducción de la carrera (de dónde salen los datos)

La carrera es **pasada**: HU-04 la descarga una vez de OpenF1 a
`eventos_carrera` y `POST /ingest/replay` la "vuelve a vivir" emitiendo los
eventos por WebSocket con sus intervalos originales (`factor` = velocidad).
Para que HU-07 pueda liquidar, la reproducción deja dos rastros con el reloj
**real** del servidor (`services/motor/internal/ingest/replay.go`):

1. **`emitido_en`** en cada evento de `eventos_carrera`, al emitirlo.
2. **Latido** en la colección `reproducciones` (un documento por carrera,
   `_id = carrera_id`): `iniciada_en` al arrancar, `latido_en` cada 5 s mientras
   corre y `terminada_en` al acabar. Una reproducción nueva de la misma carrera
   reemplaza el documento.

Además, si no se indica `offset_segundos`, la reproducción **empieza en un
punto aleatorio de la carrera**, entre 10 min después de la primera posición
y 15 min antes de la última, para no arrancar con la parrilla de salida.

Cada evento tiene tres fechas:

| Campo | Qué es | Quién lo usa |
| ----- | ------ | ------------ |
| `timestamp` | Momento del evento en la carrera (`date` de OpenF1). Histórico. | La reproducción, para ordenar y espaciar. |
| `leido_en` | Hora real en que HU-04 lo descargó. | Auditoría. |
| `emitido_en` | Hora real en que la reproducción lo emitió. | **HU-07**, para la ventana. |

### Cómo correr la demo

1. Ingesta (una vez): `POST /ingest/openf1` con la **misma `carrera_id` que
   usará el mercado**. El mercado toma la carrera más reciente de la colección
   `carreras`, que hoy solo llena el seed (`2024_monza`); HU-04 todavía no
   registra ahí las carreras que ingiere.
2. `POST /ingest/replay` con `{"carrera_id": "2024_monza", "factor": 1}` y
   dejarla corriendo (dura como máximo 30 min).
3. Abrir el mercado desde `/admin`, apostar, esperar 30 s + 60 s.
4. El mercado pasa a `liquidado` con `resultado` "Sí" o "No" y los puntos.

Con `factor` > 1 los 60 s de observación cubren más carrera (con `factor=10`,
10 minutos) y casi siempre gana "Sí". Para la demo se recomienda `factor=1`.

## Responsabilidades y contrato interno

- **Motor / MongoDB:** cierra y observa mercados, conserva los eventos de
  carrera, la reproducción y los contextos de apuesta, resuelve el tipo de
  mercado, coordina los reintentos y persiste el resultado del mercado y los
  contextos.
- **Pagos / PostgreSQL:** mantiene la apuesta financiera, saldo de puntos y
  ledger. El débito y la liquidación actualizan sus datos dentro de una
  transacción local a PostgreSQL. No existe una transacción distribuida entre
  PostgreSQL y MongoDB.
- Motor→Pagos requiere `INTERNAL_API_TOKEN` configurado en ambos servicios,
  de al menos 32 caracteres aleatorios y enviado en
  `X-Internal-Service-Token`. No se acepta una ruta interna sin ese token.
- `POST /internal/debit` recibe el ID de apuesta, usuario, mercado, opción y
  monto, además del header `Idempotency-Key`; crea la apuesta, debita tokens
  y registra el débito en un ledger de forma atómica. Responde con
  `{bet_id, status, saldo, nueva}`.
- `POST /internal/settlements` recibe el ID de apuesta, resultado (`won` o
  `lost`) y puntos, más `Idempotency-Key`. Pagos valida la fórmula y registra
  el estado final y el movimiento de liquidación atómicamente. La misma clave
  y el mismo contenido devuelven el resultado existente; contenido distinto
  se rechaza. Responde con
  `{bet_id, status, points_awarded, points_total, nueva}`.

El Motor reclama cada mercado vencido mediante una actualización condicional
con lease. Si Pagos confirma una apuesta y falla la escritura posterior en
MongoDB, el mercado no se marca como liquidado: el reintento usa la misma
clave de Pagos, recupera el resultado existente y completa MongoDB. El lease
permite recuperar mercados si el proceso cae mientras los reclama.

## Decisiones

1. **Mercado Sí/No en lugar de "qué piloto va primero".** La versión anterior
   (`driver_first_position`) exigía una "instantánea completa" de todos los
   pilotos con el mismo timestamp al milisegundo. OpenF1 publica cada cambio de
   posición como una fila suelta, así que esa instantánea casi nunca existe a
   mitad de carrera y el mercado quedaba sin liquidar. "¿Hubo algún cambio?"
   se responde directamente con esos datos.
2. **Ventana medida con `emitido_en` (opción B) y no con `timestamp`.** La
   carrera es pasada: `timestamp` es de 2024 y nunca cae dentro de una
   ventana medida con el reloj del servidor. Se descartó guardar un "reloj de
   carrera" (traducir la ventana a tiempo de carrera) por ser más complejo; con
   `emitido_en` el resolver compara fechas reales, igual que el resto del ciclo
   del mercado (RNF-05).
3. **"No" exige latido.** Sin eventos, "No" sería indistinguible de "la
   reproducción no estaba corriendo". Para no pagar "No" por error, se exige
   que la reproducción haya estado viva toda la ventana; si no, el mercado
   queda pendiente. Por eso la liquidación de un "No" puede tardar hasta un
   latido (5 s) más.
4. **Offset aleatorio.** Empezar en la parrilla haría que casi todos los
   mercados de las primeras vueltas fueran "Sí". Se elige un punto al azar
   después de ~6 vueltas; `offset_segundos` permite fijarlo para repetir una
   prueba.
5. **La lista de pilotos no se reproduce.** Su `timestamp` es la hora de la
   ingesta (años después de la carrera): la reproducción se quedaría
   "esperando" al final, latiendo sin eventos, y daría "No" con la carrera ya
   terminada.
6. **El cierre es solo de HU-11.** El liquidador ya no cierra mercados por su
   cuenta (antes se duplicaba el cierre y el mensaje `mercado_cerrado`).

## Límites conocidos

- Los mercados se resuelven contra **su** `carrera_id`: si se reproduce otra
  carrera, no hay eventos ni latido para el mercado y queda pendiente.
- La reproducción tiene un tope de 30 min (`POST /ingest/replay`); después de
  eso, los mercados sin eventos quedan pendientes (no hay latido).
- Si se reproduce dos veces la misma carrera a la vez, la más nueva es la que
  cuenta para el latido; los `emitido_en` se sobrescriben con la última emisión.
- Un mercado o apuesta de prueba solo puede liquidarse si tiene un registro
  financiero real en Pagos. Un contexto insertado a mano en MongoDB no
  equivale a una apuesta debitada ni debe generar movimientos financieros.
- El seed (`db/mongo/init/02-seed-data.js`) no crea mercados: solo la carrera
  `2024_monza` y unos eventos. Los mercados los abre el admin desde `/admin`.
- El PDF aprobado de componentes/conectores muestra OpenF1 por WebSockets,
  mientras que la aclaración textual y la implementación HU-04 usan REST.
  Esta implementación conserva REST y no declara resuelta esa discrepancia.
- Los cambios en `db/mongo/init` solo se aplican con un volumen nuevo de
  MongoDB (`docker compose down -v`).

## Requisitos de referencia

La historia requiere liquidar después del minuto de observación, clasificar
cada apuesta, sumar los puntos indicados a quien acierta, no sumar puntos a
quien pierde, evitar dobles acreditaciones y dejar trazabilidad en el ledger.
La responsabilidad de MongoDB corresponde al Motor y los movimientos
financieros/de puntos a Pagos, comunicados por REST con idempotencia.
