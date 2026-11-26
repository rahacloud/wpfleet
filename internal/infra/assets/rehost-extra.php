<?php
// The part of a host change that wp-cli search-replace cannot do. Run with
//   wp eval-file rehost-extra.php <old-host> <new-host> [dry-run]
// by `wpfleet copy`, after search-replace.
//
// - BeTheme's page builder keeps each page in the post meta mfn-page-items as
//   base64 of a serialized array, so the old host inside it is invisible to
//   search-replace. Decode, replace in every string, encode again.
// - WPML caches its language switcher templates as serialized objects whose
//   classes wp-cli does not load, so search-replace skips them. They are only
//   a cache; deleting them makes WPML build them again with the new host.

[$old, $new] = $args;
$dry = in_array('dry-run', $args, true);

function rehost_replace($value, string $old, string $new) {
    if (is_string($value)) {
        return str_replace($old, $new, $value);
    }
    if (is_array($value)) {
        foreach ($value as $k => $v) {
            $value[$k] = rehost_replace($v, $old, $new);
        }
    }
    return $value;
}

global $wpdb;
$blogs = is_multisite() ? get_sites(['fields' => 'ids', 'number' => 0]) : [get_current_blog_id()];
foreach ($blogs as $blog) {
    if (is_multisite()) {
        switch_to_blog($blog);
    }
    $changed = 0;
    $rows = $wpdb->get_results("SELECT meta_id, meta_value FROM {$wpdb->postmeta} WHERE meta_key = 'mfn-page-items'");
    foreach ($rows as $row) {
        $raw = base64_decode($row->meta_value, true);
        if ($raw === false || strpos($raw, $old) === false) {
            continue;
        }
        $items = unserialize($raw, ['allowed_classes' => false]);
        if (!is_array($items)) {
            WP_CLI::warning("blog $blog meta $row->meta_id: not a serialized array, left alone");
            continue;
        }
        $changed++;
        if (!$dry) {
            $value = base64_encode(serialize(rehost_replace($items, $old, $new)));
            $wpdb->update($wpdb->postmeta, ['meta_value' => $value], ['meta_id' => $row->meta_id]);
        }
    }
    $caches = 0;
    foreach (['wpml_language_switcher_template_objects', 'wcml_currency_switcher_template_objects'] as $option) {
        $value = $wpdb->get_var($wpdb->prepare("SELECT option_value FROM {$wpdb->options} WHERE option_name = %s", $option));
        if ($value !== null && strpos($value, $old) !== false) {
            $caches++;
            if (!$dry) {
                delete_option($option);
            }
        }
    }
    WP_CLI::log(sprintf('blog %d: %d BeTheme pages, %d WPML caches%s', $blog, $changed, $caches, $dry ? ' (dry run)' : ''));
    if (is_multisite()) {
        restore_current_blog();
    }
}
wp_cache_flush();
