<?php
// Runs the one-time login plugin against stubbed WordPress functions.
// Usage: php harness.php <plugin> <token> <option name> <option value JSON>
// It prints what the plugin did as JSON, also when the plugin exits.

define('ABSPATH', '/var/www/html/');

[, $plugin, $token, $name, $value] = $argv;

$GLOBALS['options'] = $name === '' ? [] : [$name => json_decode($value, true)];
$GLOBALS['actions'] = [];
$GLOBALS['log'] = [];

function add_action($hook, $callback, $priority = 10) { $GLOBALS['actions'][$hook] = $callback; }
function get_option($name) { return $GLOBALS['options'][$name] ?? false; }
function delete_option($name)
{
    if (!array_key_exists($name, $GLOBALS['options'])) {
        return false;
    }
    unset($GLOBALS['options'][$name]);
    return true;
}
function get_userdata($id) { return $id === 7 ? (object) ['ID' => 7, 'user_login' => 'admin'] : false; }
function wp_set_current_user($id) { $GLOBALS['log'][] = "current:$id"; }
function wp_set_auth_cookie($id, $remember, $secure) { $GLOBALS['log'][] = "cookie:$id"; }
function is_ssl() { return true; }
function do_action($hook, ...$args) { $GLOBALS['log'][] = "action:$hook"; }
function nocache_headers() {}
function admin_url() { return 'https://blog.example.com/wp-admin/'; }
function wp_safe_redirect($url) { $GLOBALS['log'][] = "redirect:$url"; }

register_shutdown_function(function () {
    echo json_encode(['log' => $GLOBALS['log'], 'options' => array_keys($GLOBALS['options'])]);
});

$_GET['wpfleet-login'] = $token;

require $plugin;

$GLOBALS['actions']['init']();
