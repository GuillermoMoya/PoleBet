// Bloque para las pantallas y secciones que todavia no se construyen. Muestra
// el titulo y la historia de usuario pendiente con el estilo de la app, para
// que el dueño de esa historia solo reemplace el contenido.

import type { ReactNode } from "react";

interface Props {
  titulo: string;
  hu?: string; // p. ej. "HU-03"
  detalle?: string;
  children?: ReactNode; // contenido extra debajo del texto
}

export default function Pendiente({ titulo, hu, detalle, children }: Props) {
  return (
    <section className="tarjeta" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      <h1 className="titulo-pagina">{titulo}</h1>
      {hu && (
        <p style={{ margin: 0 }}>
          <span className="etiqueta">Pendiente: {hu}</span>
        </p>
      )}
      {detalle && (
        <p className="texto-secundario" style={{ margin: 0 }}>
          {detalle}
        </p>
      )}
      {children}
    </section>
  );
}
