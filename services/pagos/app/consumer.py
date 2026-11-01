"""HU2-21: consumidor AMQP de Pagos para las consultas del administrador.

Proceso aparte (contenedor `pagos-consumer` en docker-compose): se arranca con

    python -m app.consumer

Toma las consultas del administrador de la cola del Broker DE A UNA
(prefetch 1, RF-30) y responde por la cola indicada en `reply_to` con el mismo
`correlation_id`. El admin (HU2-18, Iván) publica las consultas y espera la
respuesta; si no llega en [10 s], la CLI avisa que tardó demasiado (ese timeout
lo maneja Admin, no este consumidor).

Contrato del Broker (lo define esta HU; ver schemas/consultas.py):

  - Cola de consultas: `admin.consultas` (durable). Admin publica aquí.
  - prefetch=1: una consulta a la vez, para no competir con el juego en vivo.
  - Respuesta: Pagos publica en la cola `reply_to` del mensaje, con el mismo
    `correlation_id` (patrón petición-respuesta de RabbitMQ).
  - El mensaje se confirma (ack) SIEMPRE tras intentar responder: una consulta
    no se reencola (no es una operación con efectos; si falla, el admin reintenta
    desde la CLI). Así una consulta malformada no bloquea la cola.

La conexión se reintenta si el Broker aún no está listo o se cae, para no
depender del orden de arranque de los contenedores.
"""
import logging
import os
import signal
import time

import pika
from pika.exceptions import AMQPConnectionError

from app.db import SessionLocal
from app.services.consulta_handler import procesar_consulta

log = logging.getLogger("pagos.consumer")

# Cola de consultas del administrador. Nombre acordado como contrato del Broker.
COLA_CONSULTAS = "admin.consultas"

# Reintentos de conexión al Broker (arranque y caídas).
REINTENTO_CONEXION_S = 3.0


def _url_broker() -> str:
    url = os.environ.get("RABBITMQ_URL", "").strip()
    if not url:
        raise RuntimeError("falta RABBITMQ_URL para el consumidor de Pagos")
    return url


def manejar_mensaje(canal, method, props, cuerpo: bytes) -> None:
    """Procesa una consulta y responde por reply_to. Siempre hace ack."""
    try:
        with SessionLocal() as db:
            respuesta = procesar_consulta(db, cuerpo)
        reply_to = getattr(props, "reply_to", None)
        if reply_to:
            canal.basic_publish(
                exchange="",
                routing_key=reply_to,
                properties=pika.BasicProperties(
                    correlation_id=getattr(props, "correlation_id", None),
                    content_type="application/json",
                ),
                body=respuesta,
            )
        else:
            # Sin reply_to no hay a quién responder; se registra y se descarta.
            log.warning("consulta sin reply_to; se descarta la respuesta")
    except Exception:  # noqa: BLE001 - nunca dejar caer el consumidor
        log.exception("fallo al manejar una consulta")
    finally:
        # Ack siempre: la consulta no se reencola (sin efectos secundarios).
        canal.basic_ack(delivery_tag=method.delivery_tag)


def _consumir_una_vez(url: str) -> None:
    """Abre la conexión, declara la cola y consume hasta que se corte."""
    parametros = pika.URLParameters(url)
    conexion = pika.BlockingConnection(parametros)
    try:
        canal = conexion.channel()
        canal.queue_declare(queue=COLA_CONSULTAS, durable=True)
        canal.basic_qos(prefetch_count=1)  # RF-30: de a una
        canal.basic_consume(queue=COLA_CONSULTAS, on_message_callback=manejar_mensaje)
        log.info("consumidor de Pagos escuchando en %s (prefetch 1)", COLA_CONSULTAS)
        canal.start_consuming()
    finally:
        try:
            conexion.close()
        except Exception:  # noqa: BLE001
            pass


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    url = _url_broker()

    seguir = {"activo": True}

    def apagar(*_):
        log.info("apagando el consumidor de Pagos...")
        seguir["activo"] = False

    signal.signal(signal.SIGTERM, apagar)
    signal.signal(signal.SIGINT, apagar)

    while seguir["activo"]:
        try:
            _consumir_una_vez(url)
        except AMQPConnectionError as e:
            if not seguir["activo"]:
                break
            log.warning("Broker no disponible (%s); reintentando en %ss", e, REINTENTO_CONEXION_S)
            time.sleep(REINTENTO_CONEXION_S)
        except Exception:  # noqa: BLE001
            log.exception("error inesperado en el consumidor; reintentando")
            time.sleep(REINTENTO_CONEXION_S)


if __name__ == "__main__":
    main()
