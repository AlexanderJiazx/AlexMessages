import { Keyboard, Platform } from "react-native";
import { KeyboardController, KeyboardEvents } from "react-native-keyboard-controller";

/**
 * Closes the keyboard and runs `then` once it has started to hide (at once
 * when it is already down).
 *
 * iOS: anything that takes the composer away has to wait for that. The
 * KeyboardGestureArea's accessory view makes react-native-keyboard-controller
 * defer the real resignFirstResponder by a frame: a pop that unmounts the
 * composer first leaves the keyboard up with no input behind it (for good),
 * and a full-screen presentation in that frame drops the keyboard without
 * its animation. Android has neither problem: it just dismisses and goes on.
 */
export function afterKeyboardCloses(then: () => void): void {
  if (Platform.OS !== "ios") {
    Keyboard.dismiss();
    then();
    return;
  }
  if (!KeyboardController.isVisible()) {
    then();
    return;
  }
  const go = () => {
    hiding.remove();
    clearTimeout(fallback);
    then();
  };
  const hiding = KeyboardEvents.addListener("keyboardWillHide", go);
  const fallback = setTimeout(go, 250);
  void KeyboardController.dismiss();
}
