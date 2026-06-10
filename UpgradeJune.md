# Complete upgrade of AlexMessage

## Problems

### The name looks bad. (At least with this fonts)

### Channels is redundant and should be removed entirely.
Just remove the entire channel feature. From both frontend and backend


### Photo preload still doesn’t work
the expected behavior is to show a loading placeholder when the image is loading (and the size of the placeholder should have the same size as the actual image), curren version show nothing and display directly when finished loading


## Feature request / UI Refresh

### Uniform Settings/Account
There should be a uniform setting/account view, display as floating sheet after opened, they should not display in sidebar like now, as this design doesn’t scale well.

### Profile photo support
Left side bar should have a new appearance/structure
Let me describe it using SwiftUI (because I never learned others)
HStack{
ProfilePhoto()
VStack{
Name()
LastMessage()
}
}
Remove the username display since it's not for average user
If last message is an attachment, display as "Attachment: (attachment_type)" where attachment_type is either image, video, or file

### Link preview
Automatically preview the link sent

### Read remarks
There should be a read remark after opponent read the message

### Other UI improvement can be done
Sender’s own message should be right aligned, there should also be a bubble for both side’s message

Remove “DIRECT MESSAGES” label as there is no channels anymore, the top of left sidebar should have a write icon, which start a new conversation


## On Design
Preserve the overall existing design style.

## On robustness
For each feature, implement it in the most stability preserving way, try to run unit test to make sure all the feature works
