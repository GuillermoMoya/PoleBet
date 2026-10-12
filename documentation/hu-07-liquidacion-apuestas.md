# HU-07 — Liquidación de apuestas (demo limitada)

## Alcance de esta demo

El cierre de un mercado al alcanzar `cierra_en` es de HU-11 (paquete
`mercados`), que también fija `observa_hasta = cierra_en +
OBSERVATION_WINDOW_SECONDS` (60 segundos por defecto) al abrirlo. HU-07 no
cierra nada: su worker consulta los mercados ya `cerrado` y los intenta
liquidar automáticamente cuando termina esa ventana.
El intervalo de observación es inclusivo en sus límites. Una muestra solo es
elegible si tanto `timestamp` (momento del evento según OpenF1) como
`leido_en` (momento en que el Motor la recibió y persistió) cumplen
`cierra_en <= instante <= observa_hasta`. Como las posiciones de pilotos son
filas de eventos independientes y no un snapshot atómico, el resolver solo
combina filas cuyo `timestamp` coincide al milisegundo (la precisión que
persiste BSON Date). Elige el timestamp más reciente que contiene una posición
válida para todos los pilotos del roster; no combina la última fila de cada
piloto si ocurrieron en instantes distintos. `leido_en` no se usa para ordenar
la clasificación, pero una muestra recibida fuera de la ventana no es elegible.

El único tipo admitido es `driver_first_position` («qué piloto ocupa la
posición 1»). El ganador debe ser el único piloto en posición 1 de esa
instantánea completa y corresponder, sin ambigüedad, a una opción del mercado.
Las posiciones duplicadas, un empate o la falta de una instantánea completa
no se resuelven. Los identificadores se relacionan mediante
los nombres completos o acrónimos del evento de pilotos de HU-04. Muestras mal
formadas se ignoran; si falta una muestra del roster, no queda un líder único,
falta la correspondencia con las opciones o no hay datos dentro de la ventana,
la liquidación no ocurre. El mercado permanece cerrado y reintentable, y
conserva un diagnóstico del último intento.

Las apuestas se marcan ganadas o perdidas. Una ganadora suma
`10 + (monto apostado × 0,5)` puntos; una perdedora suma cero. Los tokens
apostados no se devuelven en ninguno de los dos resultados. La no devolución
al ganador es el supuesto actual del prototipo (el documento de requisitos
lo presenta como supuesto abierto), no una regla universal confirmada.

## Responsabilidades y contrato interno de la demo

- **Motor / MongoDB:** cierra y observa mercados, conserva los eventos de
  carrera y contextos de apuesta, resuelve el único tipo de mercado admitido,
  coordina los reintentos y persiste el resultado del mercado y los contextos.
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

## Límites y operación

- Esto no resuelve preguntas arbitrarias ni interpreta texto libre del
  mercado. Los mercados deben declarar `tipo: "driver_first_position"` y
  presentar opciones que correspondan a pilotos reconocidos por HU-04.
- HU-04 ya consulta OpenF1 por REST/JSON para posiciones y pilotos; HU-07
  reutiliza esos eventos y no cambia el transporte. Los eventos recibidos
  por `POST /ingest/openf1` conservan sus timestamps de carrera.
- HU-04 no tiene un ciclo automático de ingestión en vivo. Para que esta
  demo tenga muestras elegibles, se debe invocar el endpoint existente
  `POST /ingest/openf1` durante el minuto de observación; si no se ingieren
  entonces, la liquidación queda pendiente y no se aceptan cargas tardías.
- Un registro de posición reciente no es una clasificación oficial final.
  Los datos históricos, tardíos, fuera del intervalo, inválidos, insuficientes
  o ambiguos no producen un resultado ni una pérdida automática: el mercado
  queda pendiente de reintento.
- El PDF aprobado de componentes/conectores muestra OpenF1 por WebSockets,
  mientras que la aclaración textual y la implementación HU-04 usan REST.
  Esta demo conserva REST y no declara resuelta esa discrepancia.
- El módulo de apertura de mercados de HU-11 no está registrado actualmente
  en Motor. La demo procesa documentos de mercado ya persistidos que lleven
  `tipo`, `cierra_en` y `observa_hasta`; la creación de mercados continúa
  siendo responsabilidad de HU-11.
- El mercado del seed empieza su ventana en el arranque de MongoDB; el
  arranque de Compose puede consumir parte o toda la ventana de 30 segundos
  antes de que Motor acepte apuestas. No hay todavía una ruta/UI de HU-11 para
  abrir un mercado nuevo durante la demo.
- Un mercado o apuesta de prueba solo puede liquidarse si tiene un registro
  financiero real en Pagos. Los contextos semilla ficticios no equivalen a
  apuestas debitadas ni deben generar movimientos financieros.
- El worker de liquidación hace polling periódico (2 s); la liquidación puede
  ocurrir hasta un intervalo de polling después de `observa_hasta`. El cierre
  no depende de ese polling: lo hace el temporizador de HU-11.

## Requisitos de referencia

La historia requiere liquidar después del minuto de observación, clasificar
cada apuesta, sumar los puntos indicados a quien acierta, no sumar puntos a
quien pierde, evitar dobles acreditaciones y dejar trazabilidad en el ledger.
La responsabilidad de MongoDB corresponde al Motor y los movimientos
financieros/de puntos a Pagos, comunicados por REST con idempotencia.
