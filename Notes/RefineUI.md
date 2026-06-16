# Refinement on UI

## UI send behavior

Currently, on the web, the message is shown in the conversation after it reached server, this introduce latency.
The correct logic should me, once the user click send button/press enter, display the message immediately in the conversation, when message sent to the server, shown with footnote "Delivered", if the message is not sent after 10 seconds, shown as red Failed to send, with a retry button.

## Reconnecting toast
Only display reconnecting toast when connection hasn't recovered for more than 3 seconds.


## Navigation title
Remove the logo, "Alex Messages", and connection status in the top left corner, replace it with a large navigation title "Messages", and use that fancy font you used for displaying user's name in chat window.

## Settings view
Current setting view sucks, because you misunderstood me back when we make this design.

First, move the trigger to the left bottom corner, and make it looks like this (I'll describe using swift ui)
HStack{
Avatar()
VStack{
Name()
UserName()
}
}

Click to get into the setting view, defaults to Profile tab.

The new Setting view is still an overlay, but it should be a vertical tabs based view like Apple's navigation view on mac
On the left there's "Settings" title, and tabs below, on the right there is the content of current tab

Below I described tabs and their content
### Profile
Avatar
Name
Bio
Avatar and name should be centered, and the change photo should be a icon only button placed on the edge of the bottom-right edge of the circular avatar. (when there is a photo present, two options should be shown when clicked, delete photo or upload photo, if user has no photos, just make it upload photo)

Add a Cancel/Save button on the right buttom of the view when user made some changes

### Account
Account info
Password reset
Log out
Delete account (confirmation + password verification)

### Notifications
Notification push setting

### Data
Exports data options

### Admin (only for admins)
Redirect to admin panel when clicked


Mobile optimization: On low width screen, the tabs should be placed on the top, horizontally, scrollable.

For icon, use icons from the same set.

Some of those changes needs backend change to function correctly, implement the needed backend components also.

# UI
All the UI should inherits the UI design of all Alex Platform product, consistent, elegant, intuitive.

# Tests
Test all the changes yourself.