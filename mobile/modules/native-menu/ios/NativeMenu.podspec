Pod::Spec.new do |s|
  s.name           = 'NativeMenu'
  s.version        = '1.0.0'
  s.summary        = 'UIKit context menus and glass menu buttons for Alex Messages'
  s.description    = 'UIContextMenuInteraction on React Native views (no SwiftUI hosting) and a UIKit glass menu button.'
  s.author         = 'Alex Messages'
  s.homepage       = 'https://messages.alexanderjia.com'
  s.license        = 'MIT'
  s.platforms      = { :ios => '16.4' }
  s.source         = { git: '' }
  s.static_framework = true

  s.dependency 'ExpoModulesCore'

  s.source_files = '**/*.{h,m,swift}'
end
