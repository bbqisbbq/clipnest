using System;
using Windows.Media.Ocr;
class P {
  static int Main(string[] a) {
    try {
      foreach (var l in OcrEngine.AvailableRecognizerLanguages) Console.WriteLine(l.LanguageTag);
      return 0;
    } catch (Exception e) { Console.Error.WriteLine(e); return 1; }
  }
}
