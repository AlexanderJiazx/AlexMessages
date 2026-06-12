# Minor changes to Alex Messages

Name should be changed from AlexMessage to Alex Messages
Online status redundant on index.html, my suggestion: move the online status on the right to under the recipient name to replace that plain text one

Image preview on message bar just like iMessage

Make the message bar long-pill shaped, just like iMessage or any other messaging app does, and in sync to that, make the send button circle, also made the highlighting of that add attachment plus button circle.

Message bar alignment: Current alignment has no significant problem, but the send button, add button, and text are little, very little bit, off. They didn't quite align into the vertical center of the message bar
Use Lucide arrow-up for send button, also remove the “Send” text, make it icon only

Message editing
A button inline with reply and copy (but only for user’s own message), used to edit a specific message. The edit should be inline, with a confirm and cancel button. 

# Big changes to AlexMessage Call
Rebrand AlexMessage Call to Alex Meet, and do a complete upgrade, provide both video and audio services
Provide a drop-down menu on the main page; the user can choose Standard WebRTC (“Default experience”) or VolceEngine WebRTC(“Better performance in China”).

Note that the difference is purely in backend, they should have the same UI and experience for users.
For WebRTC, just use WebRTC for video/audio call, make sure you handle the backend correctly and make sure the experience is stable (because the current old version of AlexMessage Call doesn't work well)
For VolceEngine WebRTC, this is more complex, because you need to call VolceEngine SDK/API, please check their API documentation
https://www.volcengine.com/docs/6348/66812
My AppId: 6a2b39c655bc950177ce22c0
My AppKey: 41353f9216a74e3fb1869164910dd5c6

Make sure the API works seamlessly from frontend to backend.

Both backend should support multi-person meeting, expect 4-5 people in practice

Alex Meet UI UX design
Alex Meet’s overall UI style should inherit the UI design of Alex Messages and legacy AlexMessage Call.
The user experience should be similar to Google Meet, where users can create or join a meeting.
After enter the meeting, there should be a microphone/camera control (also let the user choose input source, not just turn on/turn off), share screen, and end call. Each button should be a pill shape or circle(for minor action like link share, people list) with a icon in it, all of these button sit on the bottom of the view, most button align to the left, but end call button align to the right.

On the top left corner, there should be a share button that copies the meeting link.
When there are multiple people present, their preview should be stacked vertically on the very right of the screen.
When there are multiple people present, you can view them individually by click into their preview on the stack, or show all of them (max 9 people per page) in a group view, make sure they stack smartly in group view so no space is wasted.

The main view, which is currently selected person, is shown with a rounded corner window.

On the top-right corner is a people list button, when clicked, it shows all people currently in the meeting.

The person who starts the meeting is the host, and the host can mute others. Or transfer host right

All the buttons should be icon buttons. Keep using embedded Lucide icon.

This is complex UI/UX design, handle it gracefully.
Also, it's extremely important to ensure UI consistency.
