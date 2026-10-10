// Preparacion comun de las pruebas de Vitest: agrega los matchers de
// Testing Library (toBeInTheDocument, etc.) y limpia el DOM entre pruebas.
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => cleanup());
