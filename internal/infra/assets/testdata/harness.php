<?php
// Runs rehost-extra.php against stubbed WordPress and wp-cli functions.
// Usage: php harness.php <script> <state JSON file> <wp-cli args...>
// The state is {"multisite": bool, "blogs": {"<id>": {"postmeta": [...],
// "options": {...}}}}, where a postmeta row is {"meta_id": n, "items": <any>}
// (stored as base64 of the PHP-serialized value, as BeTheme does) or
// {"meta_id": n, "raw": "<stored as is>"}. It prints the state afterwards,
// every postmeta row with its stored value and, when that decodes, its items,
// plus the wp-cli log and whether the cache was flushed.

[, $script, $stateFile] = $argv;

$state = json_decode(file_get_contents($stateFile), true);
$GLOBALS['multisite'] = $state['multisite'];
$GLOBALS['current_blog'] = (int) array_key_first($state['blogs']);
$GLOBALS['postmeta'] = [];
$GLOBALS['options'] = [];
foreach ($state['blogs'] as $id => $blog) {
    foreach ($blog['postmeta'] as $row) {
        $value = array_key_exists('raw', $row) ? $row['raw'] : base64_encode(serialize($row['items']));
        $GLOBALS['postmeta'][$id][$row['meta_id']] = $value;
    }
    $GLOBALS['options'][$id] = $blog['options'];
}
$GLOBALS['log'] = [];
$GLOBALS['flushed'] = false;

class WP_CLI
{
    public static function log($msg) { $GLOBALS['log'][] = $msg; }
    public static function warning($msg) { $GLOBALS['log'][] = "Warning: $msg"; }
}

class FakeWpdb
{
    public $postmeta = 'wp_postmeta';
    public $options = 'wp_options';

    public function get_results($sql)
    {
        if (strpos($sql, "meta_key = 'mfn-page-items'") === false) {
            throw new Exception("unexpected query: $sql");
        }
        $rows = [];
        foreach ($GLOBALS['postmeta'][$GLOBALS['current_blog']] ?? [] as $id => $value) {
            $rows[] = (object) ['meta_id' => $id, 'meta_value' => $value];
        }
        return $rows;
    }

    public function update($table, $data, $where)
    {
        $GLOBALS['postmeta'][$GLOBALS['current_blog']][$where['meta_id']] = $data['meta_value'];
        return 1;
    }

    public function prepare($sql, ...$args) { return [$sql, $args]; }

    public function get_var($prepared)
    {
        [, $args] = $prepared;
        return $GLOBALS['options'][$GLOBALS['current_blog']][$args[0]] ?? null;
    }
}

$GLOBALS['wpdb'] = new FakeWpdb();

function is_multisite() { return $GLOBALS['multisite']; }
function get_sites($query) { return array_map('intval', array_keys($GLOBALS['postmeta'] + $GLOBALS['options'])); }
function get_current_blog_id() { return $GLOBALS['current_blog']; }
function switch_to_blog($id) { $GLOBALS['previous_blog'] = $GLOBALS['current_blog']; $GLOBALS['current_blog'] = $id; }
function restore_current_blog() { $GLOBALS['current_blog'] = $GLOBALS['previous_blog']; }
function delete_option($name) { unset($GLOBALS['options'][$GLOBALS['current_blog']][$name]); return true; }
function wp_cache_flush() { $GLOBALS['flushed'] = true; }

// wp eval-file runs the file inside a function, with its arguments in $args.
(function (string $script, array $args) {
    require $script;
})($script, array_slice($argv, 3));

$out = ['log' => $GLOBALS['log'], 'flushed' => $GLOBALS['flushed'], 'blogs' => []];
foreach ($GLOBALS['options'] as $id => $options) {
    $rows = [];
    foreach ($GLOBALS['postmeta'][$id] ?? [] as $metaId => $value) {
        $raw = base64_decode($value, true);
        $items = $raw === false ? null : @unserialize($raw, ['allowed_classes' => false]);
        $rows[(string) $metaId] = ['value' => $value, 'items' => is_array($items) ? $items : null];
    }
    $out['blogs'][(string) $id] = ['postmeta' => (object) $rows, 'options' => (object) $options];
}
echo json_encode($out);
