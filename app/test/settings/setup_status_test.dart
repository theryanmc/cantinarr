import 'package:cantinarr/features/settings/data/setup_status_service.dart';
import 'package:flutter_test/flutter_test.dart';

Map<String, dynamic> _item(String key, bool configured, {bool optional = true}) =>
    {
      'key': key,
      'title': key,
      'description': 'about $key',
      'configured': configured,
      'optional': optional,
    };

SetupStatus _status(List<Map<String, dynamic>> items) => SetupStatus.fromJson({
      'items': items,
      'configured': items.where((i) => i['configured'] == true).length,
      'total': items.length,
    });

void main() {
  group('missingCoreCapability', () {
    test('a movies-only server is not broken', () {
      final status = _status([
        _item('radarr', true, optional: false),
        _item('sonarr', false, optional: false),
        _item('tmdb', true, optional: false),
      ]);

      // Sonarr is an unconfigured essential, and that is fine: the admin does
      // not do TV. Grading the rows rather than the capability would paint a
      // working server red forever.
      expect(status.remaining, 1);
      expect(status.missingCoreCapability, isFalse);
    });

    test('a books-only server is not broken', () {
      final status = _status([
        _item('radarr', false, optional: false),
        _item('sonarr', false, optional: false),
        _item('tmdb', true, optional: false),
        _item('books', true),
      ]);

      expect(status.missingCoreCapability, isFalse);
    });

    test('no library at all is broken', () {
      final status = _status([
        _item('radarr', false, optional: false),
        _item('sonarr', false, optional: false),
        _item('tmdb', true, optional: false),
        _item('books', false),
      ]);

      expect(status.missingCoreCapability, isTrue);
    });

    test('no metadata is broken even with a library', () {
      final status = _status([
        _item('radarr', true, optional: false),
        _item('tmdb', false, optional: false),
      ]);

      expect(status.missingCoreCapability, isTrue);
    });

    test('a failed load is never called broken', () {
      // No items means the request failed or the server is older, not that the
      // admin configured nothing.
      expect(SetupStatus.fromJson(const {}).missingCoreCapability, isFalse);
    });
  });
}
