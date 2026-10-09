import ExpoModulesCore
import UIKit

/// Full-screen image viewer for message attachments, in UIKit: the picture
/// flies out of its bubble at once (the bubble's own decoded image, swapped
/// for the full-resolution file when it arrives), pinches and double-taps to
/// zoom in a UIScrollView, follows a downward swipe and lands back in the
/// bubble, and shares. Tapping toggles the chrome, as in Photos.
///
/// QuickLook was tried first: it hides the tapped view while it loads its own
/// copy of the file, then zooms out of a snapshot that misses the React
/// Native image — a blank box faded into the picture, and a ghost on close.
public final class ImagePreviewModule: Module {
  public func definition() -> ModuleDefinition {
    Name("ImagePreview")

    /// `sourceTag` is the React tag of the tapped image's box.
    AsyncFunction("preview") { (url: String, sourceTag: Int?, name: String?) in
      guard let remote = URL(string: url) else {
        throw InvalidPreviewURLException(url)
      }
      guard let presenter = self.appContext?.utilities?.currentViewController() else {
        throw NoPresenterException()
      }
      let source = sourceTag.flatMap { self.appContext?.findView(withTag: $0, ofType: UIView.self) }
      ImageViewerController.present(url: remote, name: name, source: source, from: presenter)
    }
    .runOnQueue(.main)
  }
}

final class InvalidPreviewURLException: GenericException<String> {
  override var reason: String { "Invalid image URL: \(param)" }
}

final class NoPresenterException: Exception {
  override var reason: String { "No view controller to present the image from" }
}

private final class ImageViewerController: UIViewController, UIScrollViewDelegate, UIGestureRecognizerDelegate {
  private let url: URL
  private let name: String?
  /// The tapped box; hidden while the viewer is up (the picture is "out").
  private weak var source: UIView?
  private let sourceRadius: CGFloat

  private let backdrop = UIView()
  private let scroll = UIScrollView()
  private let imageView = UIImageView()
  private let closeButton = UIButton(type: .system)
  private let shareButton = UIButton(type: .system)
  private let titleLabel = UILabel()
  private var immersive = false
  private var didFlyIn = false
  private var file: URL?

  static func present(url: URL, name: String?, source: UIView?, from presenter: UIViewController) {
    presenter.view.window?.endEditing(true)
    let viewer = ImageViewerController(url: url, name: name, source: source)
    viewer.modalPresentationStyle = .overFullScreen
    // Without this the status bar keeps reporting the presenter's appearance,
    // so it stayed on screen in the black chrome-less mode.
    viewer.modalPresentationCapturesStatusBarAppearance = true
    presenter.present(viewer, animated: false)
  }

  private init(url: URL, name: String?, source: UIView?) {
    self.url = url
    self.name = name
    self.source = source
    sourceRadius = source?.layer.cornerRadius ?? 0
    super.init(nibName: nil, bundle: nil)
    imageView.image = source.flatMap(Self.displayedImage(in:))
  }

  required init?(coder: NSCoder) {
    fatalError("init(coder:) is not supported")
  }

  // Black mode hides the status bar too, like Photos' chrome tap-toggle;
  // `modalPresentationCapturesStatusBarAppearance` (set at present) routes the
  // query here instead of to the presenter.
  override var prefersStatusBarHidden: Bool { immersive }
  override var preferredStatusBarStyle: UIStatusBarStyle {
    immersive ? .lightContent : .default
  }

  /// The decoded picture React Native is showing inside `view`.
  private static func displayedImage(in view: UIView) -> UIImage? {
    if let imageView = view as? UIImageView, let image = imageView.image { return image }
    for sub in view.subviews {
      if let image = displayedImage(in: sub) { return image }
    }
    return nil
  }

  // MARK: - Views

  override func viewDidLoad() {
    super.viewDidLoad()
    view.backgroundColor = .clear

    backdrop.backgroundColor = .systemBackground
    backdrop.alpha = 0
    view.addSubview(backdrop)

    scroll.delegate = self
    scroll.showsVerticalScrollIndicator = false
    scroll.showsHorizontalScrollIndicator = false
    scroll.contentInsetAdjustmentBehavior = .never
    scroll.decelerationRate = .fast
    scroll.isHidden = true
    view.addSubview(scroll)

    imageView.contentMode = .scaleAspectFit
    imageView.isUserInteractionEnabled = true
    scroll.addSubview(imageView)

    configure(closeButton, symbol: "xmark", label: "Close")
    closeButton.addTarget(self, action: #selector(close), for: .touchUpInside)
    configure(shareButton, symbol: "square.and.arrow.up", label: "Share")
    shareButton.addTarget(self, action: #selector(share), for: .touchUpInside)
    shareButton.isEnabled = false
    titleLabel.text = name
    titleLabel.font = .preferredFont(forTextStyle: .headline)
    titleLabel.textColor = .label
    titleLabel.textAlignment = .center
    for chrome in [closeButton, shareButton, titleLabel] as [UIView] {
      chrome.alpha = 0
      view.addSubview(chrome)
    }

    let doubleTap = UITapGestureRecognizer(target: self, action: #selector(onDoubleTap(_:)))
    doubleTap.numberOfTapsRequired = 2
    scroll.addGestureRecognizer(doubleTap)
    let tap = UITapGestureRecognizer(target: self, action: #selector(onTap))
    tap.require(toFail: doubleTap)
    scroll.addGestureRecognizer(tap)
    let pan = UIPanGestureRecognizer(target: self, action: #selector(onPan(_:)))
    pan.delegate = self
    view.addGestureRecognizer(pan)

    load()
  }

  private func configure(_ button: UIButton, symbol: String, label: String) {
    var config: UIButton.Configuration
    if #available(iOS 26.0, *) {
      config = .glass()
    } else {
      config = .gray()
    }
    config.cornerStyle = .capsule
    config.image = UIImage(
      systemName: symbol,
      withConfiguration: UIImage.SymbolConfiguration(pointSize: 17, weight: .semibold)
    )
    config.baseForegroundColor = .label
    button.configuration = config
    button.accessibilityLabel = label
  }

  override func viewDidLayoutSubviews() {
    super.viewDidLayoutSubviews()
    backdrop.frame = view.bounds
    if scroll.frame.size != view.bounds.size {
      scroll.frame = view.bounds
      layoutImage()
    }
    let top = view.safeAreaInsets.top + 8
    closeButton.frame = CGRect(x: 16, y: top, width: 44, height: 44)
    shareButton.frame = CGRect(x: view.bounds.width - 60, y: top, width: 44, height: 44)
    titleLabel.frame = CGRect(x: 72, y: top, width: view.bounds.width - 144, height: 44)
  }

  /// The picture fitted to the screen at zoom 1.
  private func fittedSize() -> CGSize {
    let bounds = view.bounds.size
    guard let size = imageView.image?.size, size.width > 0, size.height > 0 else { return bounds }
    let scale = min(bounds.width / size.width, bounds.height / size.height)
    return CGSize(width: size.width * scale, height: size.height * scale)
  }

  private func layoutImage() {
    scroll.zoomScale = 1
    let fitted = fittedSize()
    imageView.frame = CGRect(origin: .zero, size: fitted)
    scroll.contentSize = fitted
    // Up to the image's own pixels (at least 3x).
    let pixels = (imageView.image?.size.width ?? 0) * (imageView.image?.scale ?? 1)
    scroll.maximumZoomScale = max(3, pixels / max(fitted.width, 1) / UIScreen.main.scale)
    centerImage()
  }

  private func centerImage() {
    let size = scroll.bounds.size
    let content = scroll.contentSize
    scroll.contentInset = UIEdgeInsets(
      top: max(0, (size.height - content.height) / 2),
      left: max(0, (size.width - content.width) / 2),
      bottom: 0,
      right: 0
    )
  }

  func viewForZooming(in scrollView: UIScrollView) -> UIView? {
    imageView
  }

  func scrollViewDidZoom(_ scrollView: UIScrollView) {
    centerImage()
  }

  // MARK: - Loading

  private func load() {
    URLSession.shared.dataTask(with: url) { [weak self] data, response, _ in
      guard let self, let data else { return }
      if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) { return }
      let image = UIImage(data: data)?.preparingForDisplay()
      let file = self.save(data)
      DispatchQueue.main.async {
        self.file = file
        self.shareButton.isEnabled = file != nil
        guard let image else { return }
        let first = self.imageView.image == nil
        self.imageView.image = image
        // Same picture, sharper: only re-fit when nothing was showing yet.
        if first || self.scroll.zoomScale <= self.scroll.minimumZoomScale {
          self.layoutImage()
        }
        if first, !self.didFlyIn { self.flyIn() }
      }
    }.resume()
  }

  /// The downloaded file under tmp/ImagePreview/<uuid>/, named after the
  /// attachment, for sharing.
  private func save(_ data: Data) -> URL? {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("ImagePreview", isDirectory: true)
    try? FileManager.default.removeItem(at: root)
    let dir = root.appendingPathComponent(UUID().uuidString, isDirectory: true)
    var fileName = (name?.isEmpty == false ? name! : url.lastPathComponent)
      .replacingOccurrences(of: "/", with: "-")
    if (fileName as NSString).pathExtension.isEmpty, !url.pathExtension.isEmpty {
      fileName += "." + url.pathExtension
    }
    let file = dir.appendingPathComponent(fileName)
    do {
      try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
      try data.write(to: file)
      return file
    } catch {
      return nil
    }
  }

  // MARK: - Transitions

  override func viewDidAppear(_ animated: Bool) {
    super.viewDidAppear(animated)
    // Without the bubble's picture there is nothing to fly yet: load() flies
    // in once the download is decoded.
    if imageView.image != nil, !didFlyIn { flyIn() }
  }

  /// A stand-in for the picture while it moves between the bubble and the
  /// viewer, so the scroll view's zoom state never has to animate.
  private func makeFlyer(frame: CGRect, radius: CGFloat) -> UIImageView {
    let flyer = UIImageView(image: imageView.image)
    flyer.contentMode = .scaleAspectFill
    flyer.clipsToBounds = true
    flyer.layer.cornerRadius = radius
    flyer.layer.cornerCurve = .continuous
    flyer.frame = frame
    view.insertSubview(flyer, aboveSubview: scroll)
    return flyer
  }

  /// Where the bubble is on screen right now — from the presentation layers,
  /// since the thread may still be moving (it settles as the keyboard goes).
  /// The viewer fills the window, so window coordinates are the view's.
  private func sourceFrame() -> CGRect? {
    guard let source, let window = source.window else { return nil }
    if let layer = source.layer.presentation(), let windowLayer = window.layer.presentation() {
      return layer.convert(layer.bounds, to: windowLayer)
    }
    return source.convert(source.bounds, to: view)
  }

  private func flyIn() {
    didFlyIn = true
    view.layoutIfNeeded()
    let end = imageView.convert(imageView.bounds, to: view)
    guard let start = sourceFrame() else {
      scroll.isHidden = false
      scroll.alpha = 0
      UIView.animate(withDuration: 0.25) {
        self.scroll.alpha = 1
        self.backdrop.alpha = 1
        self.setChrome(visible: true)
      }
      return
    }
    let flyer = makeFlyer(frame: start, radius: sourceRadius)
    source?.isHidden = true
    UIView.animate(
      withDuration: 0.45, delay: 0, usingSpringWithDamping: 0.88, initialSpringVelocity: 0,
      options: [.allowUserInteraction]
    ) {
      flyer.frame = end
      flyer.layer.cornerRadius = 0
      self.backdrop.alpha = 1
      self.setChrome(visible: true)
    } completion: { _ in
      self.scroll.isHidden = false
      flyer.removeFromSuperview()
    }
  }

  /// Back into the bubble — from wherever the picture is now (zoomed, panned
  /// or dragged) — or a fade when the bubble is gone.
  private func flyOut(velocity: CGFloat = 0) {
    let current = imageView.convert(imageView.bounds, to: view)
    let visible = current.intersection(view.bounds)
    let target = sourceFrame()
    let flyer = makeFlyer(frame: current, radius: 0)
    scroll.isHidden = true
    let distance = max(1, abs((target?.midY ?? current.midY) - current.midY))
    UIView.animate(
      withDuration: target == nil ? 0.25 : 0.42, delay: 0, usingSpringWithDamping: 0.9,
      initialSpringVelocity: target == nil ? 0 : min(velocity / distance, 8),
      options: []
    ) {
      if let target {
        flyer.frame = target
        flyer.layer.cornerRadius = self.sourceRadius
      } else {
        flyer.frame = visible.insetBy(dx: visible.width * 0.1, dy: visible.height * 0.1)
        flyer.alpha = 0
      }
      self.backdrop.alpha = 0
      self.setChrome(visible: false)
    } completion: { _ in
      self.source?.isHidden = false
      self.dismiss(animated: false)
    }
  }

  @objc private func close() {
    flyOut()
  }

  // MARK: - Chrome

  private func setChrome(visible: Bool) {
    for chrome in [closeButton, shareButton, titleLabel] as [UIView] {
      chrome.alpha = visible ? 1 : 0
    }
  }

  /// A tap toggles the chrome and, with it, white ↔ black (as in Photos).
  @objc private func onTap() {
    immersive.toggle()
    UIView.animate(withDuration: 0.2) {
      self.setChrome(visible: !self.immersive)
      self.backdrop.backgroundColor = self.immersive ? .black : .systemBackground
      self.setNeedsStatusBarAppearanceUpdate()
    }
  }

  @objc private func share() {
    guard let file else { return }
    let sheet = UIActivityViewController(activityItems: [file], applicationActivities: nil)
    sheet.popoverPresentationController?.sourceView = shareButton
    present(sheet, animated: true)
  }

  // MARK: - Gestures

  @objc private func onDoubleTap(_ gesture: UITapGestureRecognizer) {
    if scroll.zoomScale > scroll.minimumZoomScale + 0.01 {
      scroll.setZoomScale(scroll.minimumZoomScale, animated: true)
      return
    }
    let point = gesture.location(in: imageView)
    let scale = min(scroll.maximumZoomScale, 2.5)
    let size = CGSize(width: scroll.bounds.width / scale, height: scroll.bounds.height / scale)
    scroll.zoom(
      to: CGRect(x: point.x - size.width / 2, y: point.y - size.height / 2, width: size.width, height: size.height),
      animated: true
    )
  }

  /// Swipe-to-close only at zoom 1 and only for a mostly vertical drag; a
  /// zoomed picture pans instead.
  func gestureRecognizerShouldBegin(_ gesture: UIGestureRecognizer) -> Bool {
    guard let pan = gesture as? UIPanGestureRecognizer else { return true }
    guard scroll.zoomScale <= scroll.minimumZoomScale + 0.01, !scroll.isHidden else { return false }
    let velocity = pan.velocity(in: view)
    return abs(velocity.y) > abs(velocity.x)
  }

  @objc private func onPan(_ pan: UIPanGestureRecognizer) {
    let translation = pan.translation(in: view)
    let progress = min(1, abs(translation.y) / (view.bounds.height / 2))
    switch pan.state {
    case .changed:
      let scale = 1 - 0.25 * progress
      scroll.transform = CGAffineTransform(translationX: translation.x, y: translation.y).scaledBy(x: scale, y: scale)
      backdrop.alpha = 1 - progress
      if !immersive { setChrome(visible: false) }
    case .ended, .cancelled:
      let velocity = pan.velocity(in: view).y
      if pan.state == .ended, abs(translation.y) > 100 || abs(velocity) > 800 {
        flyOut(velocity: abs(velocity))
        scroll.transform = .identity
      } else {
        UIView.animate(
          withDuration: 0.35, delay: 0, usingSpringWithDamping: 0.85, initialSpringVelocity: 0,
          options: [.allowUserInteraction]
        ) {
          self.scroll.transform = .identity
          self.backdrop.alpha = 1
          if !self.immersive { self.setChrome(visible: true) }
        }
      }
    default:
      break
    }
  }
}
