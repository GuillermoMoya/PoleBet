"""HU2-21: el callback del consumidor AMQP, con un canal falso (sin RabbitMQ).

Comprueba el patrón petición-respuesta: responde por reply_to con el mismo
correlation_id y siempre hace ack. No necesita base: se parchea
procesar_consulta para aislar la lógica de mensajería.
"""
import json
from types import SimpleNamespace

import app.consumer as consumer


class CanalFalso:
    def __init__(self):
        self.publicado = []
        self.acks = []

    def basic_publish(self, exchange, routing_key, properties, body):
        self.publicado.append((routing_key, properties, body))

    def basic_ack(self, delivery_tag):
        self.acks.append(delivery_tag)


def _props(reply_to="cola.respuesta", correlation_id="abc-123"):
    return SimpleNamespace(reply_to=reply_to, correlation_id=correlation_id)


def _method(tag=7):
    return SimpleNamespace(delivery_tag=tag)


def test_responde_por_reply_to_con_correlation_id_y_hace_ack(monkeypatch):
    monkeypatch.setattr(consumer, "procesar_consulta", lambda db, cuerpo: b'{"ok":true}')
    # No abrir base real: SessionLocal como context manager vacío.
    monkeypatch.setattr(consumer, "SessionLocal", lambda: _ctx())

    canal = CanalFalso()
    consumer.manejar_mensaje(canal, _method(tag=7), _props(), b'{"tipo":"rentabilidad"}')

    assert canal.acks == [7]  # ack siempre
    assert len(canal.publicado) == 1
    routing_key, props, body = canal.publicado[0]
    assert routing_key == "cola.respuesta"
    assert props.correlation_id == "abc-123"
    assert json.loads(body) == {"ok": True}


def test_sin_reply_to_no_publica_pero_hace_ack(monkeypatch):
    monkeypatch.setattr(consumer, "procesar_consulta", lambda db, cuerpo: b'{"ok":true}')
    monkeypatch.setattr(consumer, "SessionLocal", lambda: _ctx())

    canal = CanalFalso()
    consumer.manejar_mensaje(canal, _method(tag=3), _props(reply_to=None), b"{}")

    assert canal.acks == [3]
    assert canal.publicado == []


def test_ack_aunque_el_procesamiento_falle(monkeypatch):
    def explota(db, cuerpo):
        raise RuntimeError("boom")

    monkeypatch.setattr(consumer, "procesar_consulta", explota)
    monkeypatch.setattr(consumer, "SessionLocal", lambda: _ctx())

    canal = CanalFalso()
    consumer.manejar_mensaje(canal, _method(tag=9), _props(), b"{}")

    # No se cae y confirma el mensaje para no bloquear la cola.
    assert canal.acks == [9]
    assert canal.publicado == []


class _ctx:
    """Context manager mínimo que simula SessionLocal() sin tocar la base."""

    def __enter__(self):
        return object()

    def __exit__(self, *a):
        return False
