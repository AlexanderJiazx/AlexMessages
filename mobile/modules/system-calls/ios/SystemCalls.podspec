Pod::Spec.new do |s|
  s.name           = 'SystemCalls'
  s.version        = '1.0.0'
  s.summary        = 'The iOS system call UI for Alex Messages DM calls'
  s.description    = 'CallKit, or LiveCommunicationKit in mainland China, with the WebRTC audio session handed to the system.'
  s.author         = 'Alex Messages'
  s.homepage       = 'https://messages.alexanderjia.com'
  s.license        = 'MIT'
  s.platforms      = { :ios => '16.4' }
  s.source         = { git: '' }
  s.static_framework = true

  s.dependency 'ExpoModulesCore'
  # react-native-webrtc's WebRTC build: the audio session handoff
  # (RTCAudioSession manual audio) has to reach the same WebRTC.
  s.dependency 'JitsiWebRTC'

  s.frameworks = 'CallKit', 'AVFAudio'
  # iOS 17.4+; the app still runs on 16.4, where it is never touched.
  s.weak_frameworks = 'LiveCommunicationKit'

  s.source_files = '**/*.{h,m,swift}'
  s.resource_bundles = { 'SystemCalls' => ['Resources/*.png'] }
end
