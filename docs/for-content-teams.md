# wpfleet for content teams

This guide is for the people who write, edit and publish on the websites: editors, marketing teams and site owners. It explains what you will be able to do in wpfleet's web page, in plain words.

> **This describes a web page that is still being built.** Everything below is planned and written from wpfleet's design. It is not available yet, and some details may change before it is. Until then, your platform team does these things for you.

## What wpfleet is for you

Your company's WordPress websites run on servers that a platform team looks after. wpfleet is the tool they use to run all of them in the same way. Its web page gives you the everyday actions you need, without passwords to remember and without waiting for someone to do them for you.

Each website usually exists more than once:

- **Development**, where new designs and plugins are tried first.
- **Staging**, a private copy where you check changes before anyone else sees them.
- **Live** (also called production), the website your visitors see.

## Sign in

Open the wpfleet address your platform team gives you, for example `https://wpfleet.example.com`, and sign in with your company account, the same one you use for email and other work tools. There is no separate wpfleet password.

After you sign in, you see only the websites you work on. Your platform team decides who can see which website and do which actions, based on the team you belong to in the company account. A button you are not allowed to use is not shown at all. If you expect a website or a button and do not see it, ask your platform team.

Your sign-in lasts a working day (8 hours by default). After that, sign in again.

The page is available in English and Persian. It follows your browser's language, and you can change it.

## Open a website's admin without a password

Press **Open admin** next to a website. wpfleet takes you straight into that website's WordPress dashboard, already signed in. You do not need, and never see, a WordPress password.

If the website has a WordPress administrator account with your work email address, you are signed in as that account, so the website's own records show your name on what you change. If it does not, you are signed in as the website's main administrator; ask your platform team to add an account for you if you want your own name on your changes.

Each **Open admin** works once, for a minute. If you need to go in again later, press the button again.

## Publish staging to live

When a change is ready on staging, for example a new theme version or an updated plugin, **Publish staging to live** puts the same version on the live website. You see the progress as it happens, and the page tells you when the live website is ready.

Only people whose team is allowed to publish see this button. Every publication is recorded with your name, so your team can always see who published what and when.

Note that this publishes the website's design and plugins, not its posts and pages. Posts, pages and media you add in the WordPress dashboard are saved on that website straight away and are not moved by this button.

## Back up and restore

Every website is backed up automatically every night: its posts, pages, settings and uploaded media. Backups are kept for two weeks by default.

- **Back up now** makes an extra backup at once, for example before a big change.
- **Restore yesterday's backup** (or an older one from the list) puts the website back the way it was at the time of that backup. Use it with care: everything changed since that backup is replaced. To make sure nobody restores the wrong website by mistake, wpfleet asks you to type the website's name before it starts.

Restoring uploaded media from an older backup puts back every file that was changed or deleted since then; files first uploaded after that backup stay.

## Things to know

- **Plugin and theme updates do not happen in the dashboard.** The dashboard says that updates are turned off, and that is on purpose. The platform team tests each update on development and staging first, and you publish it to live with **Publish staging to live**. This keeps the live website from breaking because of an untested update.
- **Everything is recorded.** Every action, including the ones that are refused, is written to a log with your name and the time. This protects you as much as the website.
- **One action at a time.** While a website is being published, backed up or restored, wpfleet does not start a second action on the same website. The page tells you what is running; try again when it is done.

## Getting help

For anything you cannot do on the page, or if something does not look right, contact your platform team. Tell them the website, whether it was development, staging or live, and what you pressed.
