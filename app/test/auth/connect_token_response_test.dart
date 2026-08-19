import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('ConnectTokenResponse', () {
    test('parses origin_source', () {
      final resp = ConnectTokenResponse.fromJson(const {
        'link': 'cantinarr://connect?token=abc&server=https%3A%2F%2Fx',
        'expires_at': '2026-08-26T00:00:00Z',
        'origin_source': 'external_address',
      });
      expect(resp.originSource, 'external_address');
    });

    test('an older server sends no origin_source, which must not hint', () {
      final resp = ConnectTokenResponse.fromJson(const {
        'link': 'cantinarr://connect?token=abc&server=http%3A%2F%2Fx',
        'expires_at': '2026-08-26T00:00:00Z',
      });
      expect(resp.originSource, '');
    });
  });
}
