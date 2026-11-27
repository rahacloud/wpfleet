# wpfleet for content teams

This guide is for the people who write, edit and publish on the websites: editors, marketing teams and site owners. It explains, in plain words, what you can do on wpfleet's web page.

Your organization may show the page under its own name, logo and colors, so it may not say "wpfleet" at the top. The line at the bottom of every page, "Powered by wpfleet", tells you it is this tool.

## What wpfleet is for you

Your organization's WordPress websites run on servers that a platform team looks after. wpfleet is the tool they use to run all of them in the same way. Its web page gives you the everyday actions you need, without passwords to remember and without waiting for someone to do them for you.

Each website usually exists more than once:

- **Development**, where new designs and plugins are tried first.
- **Staging**, a private copy where you check changes before anyone else sees them.
- **Production**, the website your visitors see. The page marks it **Live**.

Your platform team may use other names for these. The page always uses the names they chose.

## Sign in

Open the address your platform team gives you, for example `https://wpfleet.example.com`, and press **Sign in with your company account**. Use the same account as for email and your other work tools. There is no separate wpfleet password, and you never need a WordPress password.

After you sign in, you see only the websites you work on. Your platform team decides who can see which website and do which actions, based on the team you belong to in the company account. A button you are not allowed to use is not shown at all. If you expect a website or a button and do not see it, press **Need help?** at the top of the page, or ask your platform team.

Your sign-in lasts a working day (8 hours by default). After that, sign in again. **Sign out** is at the top of the page, beside your name.

The page is in English and Persian. It follows your browser's language at first. To change it, press the other language's name at the top of the page; the page keeps your choice until you sign out. In Persian, dates are in the Solar Hijri calendar.

## Your sites

The first page, **Your sites**, has one card for each website. A card shows:

- the website's name, and **View site**, which opens the website in a new tab;
- one line for each copy you may see (live first, then staging, then development), with its status in words: **Online**, **Starting** (it is coming up, for example after a publish), **Down**, or **Status unknown** (the page could not check);
- **Open admin** on each line where you may sign in to the dashboard;
- when the last backup was made, for example "Backed up 3 hours ago", or "Could not check the backups right now" when the page could not check;
- **Backups and more**, which opens the website's own page, where you back up and restore;
- **Publish staging to production** (with your environments' names), when staging has a newer version than production and you are allowed to publish it.

When you work on more than six websites, a search box at the top finds a website by part of its name or address as you type.

Press **Backups and more**, the website's name, or the name of one of its copies, to open its own page.

## A website's page

A website's page starts with its status, its address and its last backup. Below them are the actions you may use, each with one line that says what it does:

- **Open admin** and **View site**;
- **Publish**, when the copy it publishes from has a newer version;
- **Back up now** and **Restore a backup**.

Then comes the list of backups, in words, for example "Yesterday at 03:12 UTC", with the size of each and until when it is kept. Times on the page are in UTC.

People in the platform team also see **Technical details**, a closed section with the version numbers, the release history and what a deploy would change. You do not need it.

## Open a website's admin without a password

Press **Open admin**. A new tab opens the website's WordPress dashboard, already signed in. You do not need, and never see, a WordPress password.

If the website has a WordPress administrator account with your work email address, you are signed in as that account, so the website's own records show your name on what you change. If it does not, you are signed in as the website's main administrator; ask your platform team to add an account for you if you want your own name on your changes.

Each **Open admin** works once, for a minute. If you need to go in again later, press the button again.

While a backup is being restored on a website, **Open admin** is greyed out for that website, and a line under it says why. It works again when the restore has finished. A publish or a backup does not stop it.

## Publish

When a change is ready on staging, for example a new theme version or an updated plugin, **Publish staging to production** puts the same version on the live website. It first opens a page that says which version goes where and, for the live website, that visitors see it as soon as it is done; nothing changes until you press **Publish now** there. Only people whose team is allowed to publish see this button.

Every publication is recorded with your name, so your team can always see who published what and when.

This publishes the website's design and plugins, not its posts and pages. Posts, pages and media you add in the WordPress dashboard are saved on that website straight away, and this button does not move them.

## Back up and restore

Every website is backed up automatically every night: its posts, pages, settings and uploaded media. Backups are kept for two weeks by default.

- **Back up now** makes an extra backup at once, for example before a big change.
- **Restore a backup** opens a page that says what a restore does. Choose a backup from the list, decide whether to restore the uploaded files too, and type the website's name to confirm. The name is there so that nobody restores the wrong website by mistake; capital letters and spaces around it do not matter. If it does not match, nothing is restored, and the same page comes back with your choices kept and a line under the name that says what to fix.

A restore replaces the website's posts, pages, users and plugin settings with the copy in the backup. Everything changed since that backup is lost, and a restore cannot be undone. If you may want today's content back, press **Back up now** first.

When you restore the uploaded files too, images and documents changed or deleted since the backup are put back as they were; files uploaded after the backup stay.

## While an action runs

After you press **Publish now**, **Back up now** or **Restore the site**, a page shows the steps in plain words as they happen. This can take a few minutes. You may leave the page; the work goes on.

When it ends, the page says what happened and what to do next: check the website, or try again and whom to ask. If it failed, it says what failed, for example "The backup failed."

## Things to know

- **Plugin and theme updates do not happen in the dashboard.** The dashboard says that updates are turned off, and that is on purpose. The platform team tests each update on development and staging first, and it reaches the live website with **Publish**. This keeps the live website from breaking because of an untested update.
- **Everything is recorded.** Every action, including the ones that are refused, and every sign-in and sign-out, is written to a log with your name and the time. This protects you as much as the website.
- **One change at a time.** While a website is being published, backed up or restored, wpfleet does not start a second publish, backup or restore on the same website. The page tells you what is running; try again when it is done.

## Getting help

Press **Need help?** at the top of any page, or **Contact support** where a page offers it. Your organization chooses where these lead. If neither is there, contact your platform team. Tell them the website, whether it was development, staging or live, and what you pressed.
