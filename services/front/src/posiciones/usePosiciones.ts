// Hook de HU-05: entrega las posiciones siempre actualizadas.
//
//   1. Al montar pide la foto actual por REST, para que la pantalla no quede
//      vacia mientras llega el primer mensaje.
//   2. Luego escucha el mensaje "posiciones" del WebSocket.
//   3. Si el WebSocket se cae y vuelve, pide la foto otra vez para ponerse al
//      dia con lo que paso mientras tanto.

import { useCallback, useEffect, useRef, useState } from "react";
import { TIPOS_WS, type FotoPosiciones } from "../api/tipos";
import { useWs } from "../api/WsContexto";
import { obtenerPosiciones } from "./api";
import { detectarCambios, esFoto, esMasReciente, latenciaDe, ordenar, type Cambios } from "./foto";

// Cuanto dura resaltada la fila de un piloto que cambio de posicion.
export const DURACION_RESALTADO_MS = 1500;

export type FasePosiciones = "cargando" | "lista" | "vacia" | "error";

export interface EstadoPosiciones {
  fase: FasePosiciones;
  foto: FotoPosiciones | null;
  cambios: Cambios; // pilotos a resaltar ahora mismo
  error: string | null;
  latenciaMs: number | null; // del ultimo mensaje del WebSocket
  reintentar: () => void;
}

const SIN_CAMBIOS: Cambios = new Map();

export function usePosiciones(): EstadoPosiciones {
  const { estado, suscribir } = useWs();

  const [fase, setFase] = useState<FasePosiciones>("cargando");
  const [foto, setFoto] = useState<FotoPosiciones | null>(null);
  const [cambios, setCambios] = useState<Cambios>(SIN_CAMBIOS);
  const [error, setError] = useState<string | null>(null);
  const [latenciaMs, setLatenciaMs] = useState<number | null>(null);

  const fotoActual = useRef<FotoPosiciones | null>(null); // ultima foto aplicada
  const montado = useRef(true);
  const temporizador = useRef<ReturnType<typeof setTimeout> | null>(null);
  const seCayo = useRef(false); // el WebSocket se cayo desde la ultima carga

  // Marca el desmontaje para no tocar el estado despues, y limpia el
  // temporizador del resaltado.
  useEffect(() => {
    montado.current = true;
    return () => {
      montado.current = false;
      if (temporizador.current !== null) clearTimeout(temporizador.current);
    };
  }, []);

  // Pone una foto nueva en pantalla (venga de REST o del WebSocket) y resalta
  // por un momento a los pilotos que cambiaron de puesto.
  const aplicar = useCallback((recibida: FotoPosiciones) => {
    const anterior = fotoActual.current;
    if (anterior && !esMasReciente(recibida, anterior)) return;

    const nueva = ordenar(recibida);
    fotoActual.current = nueva;
    setFoto(nueva);
    setFase(nueva.pilotos.length > 0 ? "lista" : "vacia");
    setError(null);

    const movimientos = detectarCambios(anterior, nueva);
    if (movimientos.size === 0) return;
    setCambios(movimientos);
    if (temporizador.current !== null) clearTimeout(temporizador.current);
    temporizador.current = setTimeout(() => setCambios(SIN_CAMBIOS), DURACION_RESALTADO_MS);
  }, []);

  // Pide la foto por REST. Si falla y ya hay algo en pantalla, se deja lo que
  // hay: el WebSocket la seguira actualizando.
  const cargar = useCallback(async () => {
    try {
      const recibida = await obtenerPosiciones();
      if (!montado.current) return;
      if (recibida) aplicar(recibida);
      else if (!fotoActual.current) setFase("vacia");
    } catch (causa) {
      if (!montado.current || fotoActual.current) return;
      setError(causa instanceof Error ? causa.message : "No se pudieron cargar las posiciones.");
      setFase("error");
    }
  }, [aplicar]);

  // Paso 1: carga inicial.
  useEffect(() => {
    void cargar();
  }, [cargar]);

  // Paso 2: mensajes en vivo.
  useEffect(
    () =>
      suscribir<unknown>(TIPOS_WS.posiciones, (payload) => {
        if (!esFoto(payload)) return;
        setLatenciaMs(latenciaDe(payload));
        aplicar(payload);
      }),
    [suscribir, aplicar],
  );

  // Paso 3: al volver de una caida, ponerse al dia.
  useEffect(() => {
    if (estado === "reconectando") seCayo.current = true;
    if (estado === "abierta" && seCayo.current) {
      seCayo.current = false;
      void cargar();
    }
  }, [estado, cargar]);

  // Boton "Reintentar" del estado de error.
  const reintentar = useCallback(() => {
    setFase("cargando");
    setError(null);
    void cargar();
  }, [cargar]);

  return { fase, foto, cambios, error, latenciaMs, reintentar };
}
