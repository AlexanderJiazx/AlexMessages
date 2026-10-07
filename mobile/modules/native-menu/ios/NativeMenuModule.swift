import ExpoModulesCore
import UIKit

/// UIKit menus for React Native views.
///
/// `ContextMenuView` attaches a `UIContextMenuInteraction` straight to its
/// React Native child: the system lift + menu, with no SwiftUI hosting. A
/// SwiftUI host per message bubble re-rendered every hosting view on each
/// scroll frame (they observe their ancestors' geometry) and sized itself
/// asynchronously, so rows mounted at the wrong height.
///
/// `MenuButtonView` is a UIKit glass button whose tap opens its menu (the
/// composer "+"). It shares the UIKit glass of the composer pill beside it,
/// and React Native sizes both, so the two shapes always match.
public final class NativeMenuModule: Module {
  public func definition() -> ModuleDefinition {
    Name("NativeMenu")

    View(ContextMenuView.self) {
      Events("onPressAction")

      Prop("actions") { (view: ContextMenuView, actions: [NativeMenuAction]) in
        view.actions = actions
      }
      Prop("cornerRadius") { (view: ContextMenuView, radius: Double) in
        view.cornerRadius = CGFloat(radius)
      }
      Prop("previewBackgroundColor") { (view: ContextMenuView, color: UIColor?) in
        view.previewBackgroundColor = color
      }
    }

    View(MenuButtonView.self) {
      Events("onPressAction")

      Prop("actions") { (view: MenuButtonView, actions: [NativeMenuAction]) in
        view.actions = actions
      }
      Prop("systemImage") { (view: MenuButtonView, name: String) in
        view.systemImage = name
      }
      Prop("iconSize") { (view: MenuButtonView, size: Double) in
        view.iconSize = CGFloat(size)
      }
      Prop("iconColor") { (view: MenuButtonView, color: UIColor?) in
        view.iconColor = color
      }
      Prop("label") { (view: MenuButtonView, label: String?) in
        view.label = label
      }
    }
  }
}

struct NativeMenuAction: Record {
  @Field var id: String = ""
  @Field var title: String = ""
  @Field var systemImage: String?
  @Field var destructive: Bool = false
}

private func makeMenu(_ actions: [NativeMenuAction], onPress: @escaping (String) -> Void) -> UIMenu {
  UIMenu(children: actions.map { action in
    UIAction(
      title: action.title,
      image: action.systemImage.flatMap { UIImage(systemName: $0) },
      attributes: action.destructive ? [.destructive] : []
    ) { _ in onPress(action.id) }
  })
}

/// Ends the React Native touch running under `view`. Once the menu takes over
/// a hold, the Pressable underneath must neither stay "pressed" (its pressed
/// style would show on the lifted preview) nor fire onPress on release.
private func cancelReactNativeTouches(from view: UIView) {
  guard let touchHandlerClass = NSClassFromString("RCTSurfaceTouchHandler") else { return }
  var current: UIView? = view
  while let v = current {
    if let recognizer = v.gestureRecognizers?.first(where: { $0.isKind(of: touchHandlerClass) }) {
      if recognizer.isEnabled {
        recognizer.isEnabled = false
        recognizer.isEnabled = true
      }
      return
    }
    current = v.superview
  }
}

final class ContextMenuView: ExpoView, UIContextMenuInteractionDelegate {
  let onPressAction = EventDispatcher()
  var actions: [NativeMenuAction] = []
  var cornerRadius: CGFloat = 18
  var previewBackgroundColor: UIColor?

  required init(appContext: AppContext? = nil) {
    super.init(appContext: appContext)
    addInteraction(UIContextMenuInteraction(delegate: self))
  }

  /// The lifted view: the single React Native child (this view wraps it exactly).
  private var content: UIView { subviews.first ?? self }

  func contextMenuInteraction(
    _ interaction: UIContextMenuInteraction,
    configurationForMenuAtLocation location: CGPoint
  ) -> UIContextMenuConfiguration? {
    guard !actions.isEmpty else { return nil }
    cancelReactNativeTouches(from: self)
    let actions = self.actions
    return UIContextMenuConfiguration(identifier: nil, previewProvider: nil) { [weak self] _ in
      makeMenu(actions) { id in self?.onPressAction(["id": id]) }
    }
  }

  func contextMenuInteraction(
    _ interaction: UIContextMenuInteraction,
    configuration: UIContextMenuConfiguration,
    highlightPreviewForItemWithIdentifier identifier: any NSCopying
  ) -> UITargetedPreview? {
    targetedPreview()
  }

  func contextMenuInteraction(
    _ interaction: UIContextMenuInteraction,
    configuration: UIContextMenuConfiguration,
    dismissalPreviewForItemWithIdentifier identifier: any NSCopying
  ) -> UITargetedPreview? {
    targetedPreview()
  }

  private func targetedPreview() -> UITargetedPreview? {
    let view = content
    guard view.window != nil else { return nil }
    let parameters = UIPreviewParameters()
    parameters.visiblePath = UIBezierPath(roundedRect: view.bounds, cornerRadius: cornerRadius)
    parameters.backgroundColor = previewBackgroundColor ?? .clear
    return UITargetedPreview(view: view, parameters: parameters)
  }
}

final class MenuButtonView: ExpoView {
  let onPressAction = EventDispatcher()
  private let button = UIButton(type: .system)

  var actions: [NativeMenuAction] = [] {
    didSet { updateMenu() }
  }
  var systemImage = "plus" {
    didSet { updateConfiguration() }
  }
  var iconSize: CGFloat = 18 {
    didSet { updateConfiguration() }
  }
  var iconColor: UIColor? {
    didSet { updateConfiguration() }
  }
  var label: String? {
    didSet { button.accessibilityLabel = label }
  }

  required init(appContext: AppContext? = nil) {
    super.init(appContext: appContext)
    button.showsMenuAsPrimaryAction = true
    addSubview(button)
    updateConfiguration()
    updateMenu()
  }

  override func layoutSubviews() {
    super.layoutSubviews()
    // React Native owns the size, exactly like the composer pill's.
    button.frame = bounds
  }

  private func updateConfiguration() {
    var config: UIButton.Configuration
    if #available(iOS 26.0, *) {
      config = .glass()
    } else {
      config = .plain()
    }
    config.cornerStyle = .capsule
    config.contentInsets = .zero
    // The glass configuration sizes symbols itself (large scale) and tints
    // them with its own label colour, ignoring the image's configuration and
    // `baseForegroundColor`. Pin the symbol configuration and bake the colour
    // into the image so the glyph matches the 18pt ink2 icons elsewhere.
    let symbol = UIImage.SymbolConfiguration(pointSize: iconSize, weight: .regular, scale: .medium)
    config.preferredSymbolConfigurationForImage = symbol
    var image = UIImage(systemName: systemImage, withConfiguration: symbol)
    if let iconColor {
      image = image?.withTintColor(iconColor, renderingMode: .alwaysOriginal)
    }
    config.image = image
    button.configuration = config
  }

  private func updateMenu() {
    button.menu = makeMenu(actions) { [weak self] id in self?.onPressAction(["id": id]) }
  }
}
