// Comparte una sola conexion WebSocket con toda la app.
//
// Uso en un componente:
//
//   const { estado, suscribir } = useWs();
//   useEffect(() => suscribir<FotoPosiciones>(TIPOS_WS.posiciones, setFoto), [suscribir]);

import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useAuth } from "../auth/AuthContext";
import { urlWsMotor } from "./config";
import { ClienteWs, type EstadoWs, type Manejador } from "./ws";

export interface ValorWs {
  estado: EstadoWs;
  suscribir: <T = unknown>(tipo: string, manejador: Manejador<T>) => () => void;
}

const WsContexto = createContext<ValorWs | null>(null);

interface Props {
  children: ReactNode;
  cliente?: ClienteWs; // reemplaza al cliente real en las pruebas
}

// Crea el cliente y lo conecta mientras haya sesion: el motor rechaza el
// WebSocket sin la cookie de sesion, asi que sin usuario no se intenta.
export function WsProvider({ children, cliente: clienteInyectado }: Props) {
  const { usuario } = useAuth();
  const cliente = useMemo(() => clienteInyectado ?? new ClienteWs({ url: urlWsMotor() }), [clienteInyectado]);
  const [estado, setEstado] = useState<EstadoWs>(cliente.estado);

  // Refleja el estado del cliente en React.
  useEffect(() => {
    setEstado(cliente.estado);
    return cliente.alCambiarEstado(setEstado);
  }, [cliente]);

  // Conecta al iniciar sesion y cierra al salir (o al desmontar).
  const userId = usuario?.id;
  useEffect(() => {
    if (!userId) return;
    cliente.conectar();
    return () => cliente.cerrar();
  }, [cliente, userId]);

  const valor = useMemo<ValorWs>(
    () => ({ estado, suscribir: (tipo, manejador) => cliente.suscribir(tipo, manejador) }),
    [cliente, estado],
  );

  return <WsContexto.Provider value={valor}>{children}</WsContexto.Provider>;
}

// Hook para usar la conexion compartida.
export function useWs(): ValorWs {
  const valor = useContext(WsContexto);
  if (!valor) throw new Error("useWs debe usarse dentro de <WsProvider>");
  return valor;
}
