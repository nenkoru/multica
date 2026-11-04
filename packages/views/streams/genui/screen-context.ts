import { createContext, useContext } from "react";

export interface ScreenEnv {
  /** Buttons work only once the screen is checked and stored. */
  interactive: boolean;
  /** Sends a follow-up from a screen button as the user's next message. */
  onFollowup?: (text: string) => void;
}

export const ScreenEnvContext = createContext<ScreenEnv>({ interactive: false });

export function useScreenEnv(): ScreenEnv {
  return useContext(ScreenEnvContext);
}
