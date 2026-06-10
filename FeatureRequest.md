# Feature Request

Here are some features I want you to implement in AlexMessage

### Notification Push API
Use Web Push API to push DM notification to users when their browser page is at background/inactive/not open.
Let user subscribe to the push using a pop up window, and then use the API to push notification
Check relevant documentations yourself.

### Unread message
Unread chat should have a green dot showing unread state.
The unread state should be clear after user click in to that conversation.
Again, this only applies to DM.

### Lazy loading for conversations.
Lazy loading for conversations to make sure a long conversation won’t crash/stuck the browser.

### Chat action
Each dm chat should have a three-dot dropdown menu. The menu should contain.
- Pin/Unpin(depending on current state)
- Mark as read/unread (depending on current state)
- Delete
Let me add a little bit more context for Pin
Pin: There should be a new Pinned category in the left sidebar (But it will not display if pinned list is empty). Once a message is pinned, it will no longer shown in Direct Messages, but Pinned category.

For delete, Delete only delete the conversation for that user, other user is not affected and can still see the conversation.


### Favicon
Add Favicon for AlexMessage, just use the icon you used in the main webapp.

### UI&Test
Your UI should align with existing UI design, and you should ensure the logical/graphical consistent with existing code/design.
For testing, you can use the browser use tool, take screenshot -> check it yourself.
