# Bug Fixes

In this file, I will describe the bugs/undesired behaviors I observed in this web app, and I hope you to fix them one by one.

### Image reloads problem.
Everytime a new message is sent, all the image in the converstaion will quickly disappear and showup again, this is not desired, please fix it. A common solution is to use a placeholder for image when it is not loaded (but make sure the placeholder has the same size as the image)

### Default view
The default view should not be `general` channel, but a “No conversation selected” view just like iMessage. The webapp should also preserve its last time path just like iMessage. (For example, when I chat with Tim and closed the app, next time the app should reload at my conversation with Tim.)

### Deleted users
User’s name should be shown as Deleted User after they are deleted from the system, instead of user 4, user 6 or something like that.

### Visibility
The full list of registered/online user should not be available to normal users under any circumstances, when user wants to chat with someone they hasn't chat with before, they have to start a new chat and enter the user name of the user they want to chat with.

### Join/Left notifications
joined/left notifications in group chat is a legacy from LAN app version, it should not be shown, remove it entirely

### Horizontal scrolling in mobile size 
On mobile screens, you can scroll horizontally in conversations, this should not be allowed
