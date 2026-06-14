# Refinement on Alex Meet

The current version of AlexMeet is fully functional, thanks for your effort.
But there are still some bug to fix. And some improvement can be made.

## Bug fixes / UI Display issue

### Window UI / Arrangement
On preview window arrangement, you misunderstood me a lot.
Expected behavior should be like this, in standard, non-grid view, every participant except the highlighted one is stacked on the very right of the screen, and their video ratio should NOT change! A horizontal view should still be horizontal, scale to fit, not fill.
In current version, this is handled in a weird way, the non-highlighted participants will be squeezed to a vertical view on the right, and the 3rd, 4th participants will not be displayed in the standard view.
Grid view also has some problems; it does not perform well for meetings with more than 3 people, the view will exceed the bottom line and not fully displayed.

Here’s an example of how you would handle it on normal display
For 2 people: 1 row, 2 column
For 3 people, 2 row, first row 2 people, second row 1 people
4 people: 2 x 2
5: 3row, 1 person on the last row
6: 2 row 3 column
7-9: 3 row 3 column, all people after 6 on the third row

For phones, or low-width displays, the arrangement should change to vertical display
For 2 people: 2 row, 1 column
For 3 people, 3 row, 1 column
Etc.

Note that these are just examples, do NOT hardcode it, you should adjust dynamically based on window’s ratio, eg. if there’s a window that has a 100:1 ratio, of course you will have 1 row, n column for the view.

And, the default view should be grid view, when user clicked to the preview participants, switch to focus view.

## Feature request
Kick people on people list pop-up (only for host)
Output audio device selection (put it under the same menu as microphone).
Speaking person’s preview card should have a light green stroke (don't make it too sensitive, make sure background noise won't activate it).


## Improvement

### Control button arrangement
On vertical screens like phones, let the control buttons group and end call button be center-aligned and stacked vertically (buttons in each group, e.g Control buttons still stack horizontally)

### Streaming quality improvement
Check VolcEngine WebRTC documentation, see how to configure for 1080p 4K video streaming, and premium audio quality streaming. Each user has their own quality selection button, which can choose for Auto (Recommended), Low, Standard, High Quality, and Premium quality (auto, 360p, 720p, 1080p, 4K, also pick 4 different audio quality corresponds to this, or just leave it default if audio configuration is not available), this controls the quality of their streaming.  Also apply this for Standard WebRTC.

### Allow non-registered user
A meeting can be set to allow non-registered, user to enter, and host can change this setting in People list pop-up. When log in is not required, participants don’t have to login, just have to enter their name to enter the call.

### Preview before entering the call.
On the Ready to join? page, you should provide a video preview for people, also provide audio in/out, video in source options, that way they know how they looks and configure everything correctly before enter the meeting.
