<?php
/**
 * Plugin Name: wpfleet one-time login
 * Description: Answers the one-time admin login links that `wpfleet admin` prints. Mounted by the wpfleet chart (wordpress.oneTimeLogin).
 *
 * `wpfleet admin` stores the SHA-256 of a random token in the option
 * _wpfleet_login_<sha256>, with the user ID and an expiry 60 seconds ahead.
 * A request for /?wpfleet-login=<token> finds that option, deletes it before
 * anything else so that the link works once, and logs the user in. Any other
 * token falls through to the normal page, which says nothing about why.
 */

defined('ABSPATH') || exit;

add_action('init', 'wpfleet_login', 0);

/**
 * Logs in the user of a valid one-time token, and returns otherwise.
 */
function wpfleet_login()
{
    if (!isset($_GET['wpfleet-login']) || !is_string($_GET['wpfleet-login'])) {
        return;
    }

    $token = $_GET['wpfleet-login'];

    // 32 random bytes in unpadded URL-safe base64.
    if (preg_match('/^[A-Za-z0-9_-]{43}$/', $token) !== 1) {
        return;
    }

    $hash = hash('sha256', $token);
    $name = '_wpfleet_login_' . $hash;
    $login = get_option($name);

    if (!is_array($login)) {
        return;
    }

    // Single use: only the request that deletes the option goes on, so two
    // requests racing with the same link cannot both log in.
    if (!delete_option($name)) {
        return;
    }

    if (!isset($login['hash'], $login['user'], $login['expires']) || !is_string($login['hash'])) {
        return;
    }

    if (!hash_equals($login['hash'], $hash) || (int) $login['expires'] < time()) {
        return;
    }

    $user = get_userdata((int) $login['user']);

    if (!$user) {
        return;
    }

    wp_set_current_user($user->ID);
    wp_set_auth_cookie($user->ID, false, is_ssl());
    // Lets security and audit plugins record the login.
    do_action('wp_login', $user->user_login, $user);

    nocache_headers();
    wp_safe_redirect(admin_url());
    exit;
}
