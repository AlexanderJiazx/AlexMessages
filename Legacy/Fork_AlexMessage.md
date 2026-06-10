# Fork - Alex Message

Make an fork of current project, rebrand the chat platform to be AlexMessage.

## Things you need to know
AlexMessage will be hosted on cloud, messages.alexanderjia.com
It will no longer be just a LAN message app, it will be a complete messaging experience just like whatsapp or messenger.
On AlexMessage, user is no longer identified by IP address, but accounts.
AlexMessage will include other features like direct message, contacts management, etc.

### LAN to Internet
Because AlexMessage is no longer a LAN chatroom, you have to modify parts that designed for LAN communication, and make it suitable for internet messsaging.
User is also account based, so you should handle that.

### Database
AlexMessage is a real product, so you should use proper database to store informations.
My suggestion is to use SQLite, as we prioritize lightweight backend.
Note that you should also handle password hashing properly.

### Storage
Store files in a dedicated directory, store files path/UUID in database.
Each user should get its own storage directory.This way the account deletion would be straightforward.

### Cookies
AlexMessage should handle cookies properly, ensure convinience and security.

### Control panel
AlexMessage should have a control panel for admin to operate, running on a seperate port.
This panel should need admin password for verification
Note that the UI of this control panel should be consistent with the core messaging app.
Control panel can be used to:
1.Create/Delete account
2.Edit account information, passwords.
3.Execute other global operation

### Account handling.
User should be able to register accounts themselves, but request needs to be approved by admin in control panel.

## UI
Although AlexMessage is a rebrand, but you should still follow the previous UI guideline. And you should make UI looks consistent across everywhere

## Test
AlexMessage would be a complex project, you can use your own browser to test/debug it, to make sure you are always aware of the product expeirence you created.

# Important
This is a complex rebuild, so it would be hard.
Make it work first(implement the function), then refine, this way you can always rollback if the refinement failed.
A good habit is to write small unit test, and make sure every components works with no problem, then wire them up.
