import { Clipboard } from '@wailsio/runtime';

export async function writeClipboardText(text: string) {
  try {
    await Clipboard.SetText(text);
  } catch (nativeError) {
    if (!navigator.clipboard?.writeText) {
      throw nativeError;
    }
    await navigator.clipboard.writeText(text);
  }
}
