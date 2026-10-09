import ExpoModulesCore
import UIKit

/// A progressive blur with no tint: what is behind it is blurred most at one
/// edge and fades to sharp at the other — the blur iOS draws along screen
/// edges, without the colour wash of a material or of UIKit's scroll-edge
/// effect (which fades content toward white and turned the clear glass
/// header white).
///
/// UIKit has no public variable blur. This hosts a Core Animation backdrop
/// layer (CABackdropLayer) with the "variableBlur" filter, masked by a
/// gradient — private API, looked up at runtime; without it nothing is drawn.
/// (Swapping the filters of a UIVisualEffectView's backdrop no longer works
/// on iOS 26: UIKit keeps its own uniform blur, with a hard bottom edge.)
public final class EdgeBlurModule: Module {
  public func definition() -> ModuleDefinition {
    Name("EdgeBlur")

    View(EdgeBlurView.self) {
      Prop("maxRadius") { (view: EdgeBlurView, radius: Double) in
        view.maxRadius = CGFloat(radius)
      }
      /// The edge that is blurred most: "top" or "bottom".
      Prop("edge") { (view: EdgeBlurView, edge: String) in
        view.fromTop = edge != "bottom"
      }
    }
  }
}

final class EdgeBlurView: ExpoView {
  var maxRadius: CGFloat = 12 {
    didSet { applied = nil; setNeedsLayout() }
  }
  var fromTop = true {
    didSet { applied = nil; setNeedsLayout() }
  }

  private let backdrop: CALayer? = (NSClassFromString("CABackdropLayer") as? CALayer.Type)?.init()
  private var applied: CGSize?

  required init(appContext: AppContext? = nil) {
    super.init(appContext: appContext)
    isUserInteractionEnabled = false
    if let backdrop {
      layer.addSublayer(backdrop)
    }
  }

  override func didMoveToWindow() {
    super.didMoveToWindow()
    // Sample the backdrop at the screen's resolution.
    if let window { backdrop?.setValue(window.screen.scale, forKey: "scale") }
  }

  override func layoutSubviews() {
    super.layoutSubviews()
    CATransaction.begin()
    CATransaction.setDisableActions(true)
    backdrop?.frame = bounds
    CATransaction.commit()
    if applied != bounds.size { applyFilter() }
  }

  private func applyFilter() {
    guard bounds.height > 0,
          let backdrop,
          let filterClass = NSClassFromString("CAFilter") as? NSObject.Type,
          let filter = filterClass
            .perform(NSSelectorFromString("filterWithType:"), with: "variableBlur")?
            .takeUnretainedValue() as? NSObject,
          let mask = Self.maskImage(height: bounds.height, fromTop: fromTop)
    else { return }
    filter.setValue(maxRadius, forKey: "inputRadius")
    filter.setValue(mask, forKey: "inputMaskImage")
    filter.setValue(true, forKey: "inputNormalizeEdges")
    backdrop.filters = [filter]
    applied = bounds.size
  }

  /// Alpha mask for the filter: opaque (full blur) at the blurred edge,
  /// clear (no blur) at the other.
  private static func maskImage(height: CGFloat, fromTop: Bool) -> CGImage? {
    let rows = max(2, Int(height.rounded()))
    let rgb = CGColorSpaceCreateDeviceRGB()
    // RGBA components in the context's own space (UIColor.black is a gray
    // colour: handed to an RGB gradient it came out opaque at both ends, so
    // the whole view blurred at full strength).
    guard let context = CGContext(
      data: nil, width: 1, height: rows, bitsPerComponent: 8, bytesPerRow: 0,
      space: rgb, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
    ),
      let gradient = CGGradient(
        colorSpace: rgb, colorComponents: [0, 0, 0, 1, 0, 0, 0, 0], locations: [0, 1], count: 2
      )
    else { return nil }
    // Bitmap contexts have their origin at the bottom: y = rows is the top
    // row of the image.
    let blurred = CGPoint(x: 0, y: fromTop ? CGFloat(rows) : 0)
    let sharp = CGPoint(x: 0, y: fromTop ? 0 : CGFloat(rows))
    context.drawLinearGradient(gradient, start: blurred, end: sharp, options: [])
    return context.makeImage()
  }
}
