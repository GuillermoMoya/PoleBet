import React from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { WsProvider } from "./api/WsContexto";
import App from "./App";
import { AuthProvider } from "./auth/AuthContext";
import "./estilos/tokens.css";
import "./estilos/base.css";

// Orden de los proveedores: el router primero, luego la sesion y por ultimo
// el WebSocket, que solo se conecta cuando hay sesion.
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AuthProvider>
        <WsProvider>
          <App />
        </WsProvider>
      </AuthProvider>
    </BrowserRouter>
  </React.StrictMode>,
);
