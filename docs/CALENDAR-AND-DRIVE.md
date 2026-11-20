# Calendar, booking pages and Drive: how to

Each mailbox has a calendar, booking pages and a Drive beside its mail. This
guide is for the person who holds the mailbox: how to do each thing, step by
step. What each one is, and how it keeps your data, is in
[VAYUMAIL.md](VAYUMAIL.md).

All three are in Mail's top bar: **Mail**, **Calendar** and **Drive**.

## Calendar

### Make an event

1. Open **Calendar** and choose a time in the week, month or day, or press
   **Event**.
2. Give it a title, a start and an end, or tick **All day**.
3. To repeat it, choose **Daily**, **Every weekday**, weekly or monthly, and
   when it **Ends**: never, on a **Last day**, or after **How many times**.
4. To invite people, type their addresses under **Guests**, separated by
   commas. Each gets an invitation by mail; their answers show on the event.
5. Choose a reminder (**At the start**, 5 minutes up to a week before), and
   tick **By mail too** to have it mailed as well as shown on your devices.
6. Save.

An invitation that comes to you by mail is answered from the message itself:
the reader shows the event with Accept, Maybe and Decline.

### Put it on a phone or a computer

The calendar is the same one your phone and computer keep in step over
CalDAV, so an event made in either place shows in the other.

1. In Mail, open **Connect a device** and make an app password for the
   phone or computer. It is shown once: copy it.
2. On the device, add a CalDAV account (on an iPhone: Settings › Calendar ›
   Accounts › Add Account › Other › Add CalDAV Account).
3. Server: your site's address, such as `example.com`. Nothing more is
   needed: the device finds the calendar from the name alone.
4. User name: your mail address. Password: the app password.

Contacts are added the same way, as a CardDAV account.

### Bring events in, or take them out

- **Export as .ics** in the calendar's menu saves every event as one file.
- **Import a .ics file** reads one in. An event you already have is left
  as it is, and nobody is mailed.

### Show another calendar beside yours

1. In the calendar's menu, open **Subscribe**.
2. Paste its **Address (.ics, https or webcal)** and press **Add this
   calendar**. For a Google Calendar, use its "Secret address in iCal format"
   from that calendar's settings.
3. Tick **Its events make me busy on my booking pages** if it should.

It is read again every half hour. A calendar on another CalDAV server is
added the same way, with an **App password there** from that server.

## Booking pages

A booking page is a link anyone can book a free time from. Guests see only
free times, in their own time zone, never what is in your calendar.

### Make one

1. In **Calendar**, open **Booking pages** and press **New booking page**.
2. Give it a **Name** and a **Name in the link**: the page's address is
   `https://<your site>/book/<you>/<name in the link>`.
3. Choose the **Lengths offered** and your **Weekly hours**, one line a day,
   as `10:00-13:00, 15:00-18:00`.
4. Set the **Minutes kept free before** and **after**, how many hours ahead
   a booking must be (**At least this many hours ahead**) and how far out
   (**Up to this many days out**), and the **Most bookings in a day**.
5. Optionally, ask **A question for the guest**, choose a reminder for both of
   you, and allow a series (**A guest may book the same time weekly or every
   two weeks**).
6. Tick **Taking bookings** and **Save**. Copy the link and send it.

### What happens when someone books

The guest picks a time and gets a mail to confirm it; the time is held until
they do. Once confirmed it is an event in your calendar with them as its
guest, and both of you are reminded before it. Their mail carries a link to
move or cancel. The page's **Recent bookings** lists each one, waiting for
confirmation or confirmed.

To stop taking bookings, untick **Taking bookings**; **Delete page** removes
it.

## Drive

### Upload and organise

1. Open **Drive**.
2. Drop files on the page, or choose them. A file can be up to 2 GB; a large
   one goes up in pieces, and a dropped connection is waited out.
3. **New folder** makes a folder; **Move to** moves a file or folder.
4. **Move to Trash** deletes. Trash is kept for 7 to 90 days, as **Drive
   settings** says; **Bring back** restores and **Empty Trash** deletes for
   good.

Saving over a file keeps what it was: open the file and **Earlier versions**.
**Drive settings** chooses how many versions are kept (1 to 20).

### Send a file from a message, or keep an attachment

- In **Compose**, attach a file from Drive as well as from your computer.
- In a message, **Save to Drive** beside an attachment keeps it in Drive.

### Share a file or folder

**By link**, for anyone:

1. Open the file or folder and choose **Share by link**.
2. Choose when it **Ends** (in a day, 7 or 30 days, or until you stop it),
   and optionally a **Password** and a **Most downloads**.
3. For a folder, choose what **The link lets someone** do: **Look through it
   and download**, or **Only upload into it** (an upload link, which never
   shows what is there).
4. Press **Make a link**. **This link is shown once. Copy it now.** The file
   lists each link with when it was last used, and stops one on request.

**With someone on this server:** choose the person's **Address on this
server**. For a folder, tick **Also add files and folders** to let them add.
They find it under **Shared with me** in their Drive; what they add is kept in
yours.

### Open Drive in a computer's or phone's file app

Drive is served over WebDAV. In the file app, connect to a server at
`https://<your site>/dav/files/` with your mail address and an app password
from **Connect a device**.
