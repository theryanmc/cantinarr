import 'package:dio/dio.dart';

final _workId = RegExp(r'^ol:OL[1-9][0-9]{0,11}W$');

/// Open Library metadata has no Chaptarr identity or availability snapshot.
class DiscoveryBook {
  final String foreignId;
  final String title;
  final List<String> authors;
  final int? year;
  final String description;
  final int? coverId;

  const DiscoveryBook(
      {required this.foreignId,
      required this.title,
      this.authors = const [],
      this.year,
      this.description = '',
      this.coverId});

  static bool validId(String id) => _workId.hasMatch(id);
  String get workId => foreignId.substring(3);
  String get author => authors.join(', ');
  String get searchTerm => [title, author].where((s) => s.isNotEmpty).join(' ');
  String get openLibraryUrl => 'https://openlibrary.org/works/$workId';
  String? get coverUrl =>
      coverId != null && coverId! > 0 && coverId! <= 9007199254740991
          ? 'https://covers.openlibrary.org/b/id/$coverId-M.jpg?default=false'
          : null;
  String detailLocation(String? instanceId) =>
      Uri(path: '/detail/book/$foreignId', queryParameters: {
        if (instanceId != null) 'instance_id': instanceId,
        'source': 'openlibrary'
      }).toString();

  factory DiscoveryBook.fromJson(Map<String, dynamic> json) {
    final id = json['foreign_id'] as String? ?? '';
    final title = json['title'] as String? ?? '';
    if (!validId(id) || title.trim().isEmpty || json['authors'] is! List) {
      throw const FormatException('Invalid book discovery response');
    }
    return DiscoveryBook(
        foreignId: id,
        title: title,
        authors: (json['authors'] as List).cast<String>(),
        year: json['year'] as int?,
        description: json['description'] as String? ?? '',
        coverId: json['cover_id'] as int?);
  }
}

class BookGenre {
  final String id;
  final String name;
  const BookGenre(this.id, this.name);
}

class BookDiscoveryPage {
  final List<DiscoveryBook> results;
  final int? nextPage;
  final String emptyMessage;
  const BookDiscoveryPage(this.results, this.nextPage, this.emptyMessage);
}

class BookRequestTarget {
  final String foreignId;
  final String title;
  final String author;
  const BookRequestTarget(this.foreignId, this.title, this.author);
}

class BookDiscoveryException implements Exception {
  final String message;
  final bool accessDenied;
  const BookDiscoveryException(this.message, {this.accessDenied = false});
  @override
  String toString() => message;
}

const bookDiscoveryUpdateNotice =
    'Update your Cantinarr server to discover books. Library browsing and search are still available.';

class BookDiscoveryService {
  final Dio _dio;
  BookDiscoveryService(this._dio);

  Future<Map<String, dynamic>> _get(String path, String? instanceId,
      [Map<String, dynamic> params = const {}]) async {
    try {
      final response = await _dio.get(path, queryParameters: {
        if (instanceId != null) 'instance_id': instanceId,
        ...params
      });
      if (response.data is! Map<String, dynamic>) {
        throw const FormatException('Invalid book discovery response');
      }
      return response.data as Map<String, dynamic>;
    } on DioException catch (e) {
      if (e.response?.statusCode == 401 || e.response?.statusCode == 403) {
        throw const BookDiscoveryException(
            'Books are not available for this account.',
            accessDenied: true);
      }
      if (e.response?.statusCode == 404 &&
          !path.startsWith('/api/media/book/')) {
        throw const BookDiscoveryException(bookDiscoveryUpdateNotice);
      }
      throw const BookDiscoveryException('Could not load books. Please retry.');
    }
  }

  Future<BookDiscoveryPage> feed(String feed, String? instanceId,
      {String? genre, int page = 1}) async {
    final json = await _get('/api/discover/books/$feed', instanceId,
        {'page': page, if (genre != null) 'genre': genre});
    if (json['results'] is! List ||
        json['page'] != page ||
        json['total_results'] is! int ||
        (json['next_page'] != null && json['next_page'] != page + 1)) {
      throw const FormatException('Invalid book discovery page');
    }
    return BookDiscoveryPage(
        (json['results'] as List)
            .map((b) => DiscoveryBook.fromJson(b as Map<String, dynamic>))
            .toList(),
        json['next_page'] as int?,
        json['empty_message'] as String? ??
            'No books found on this page of Open Library.');
  }

  Future<List<BookGenre>> genres(String? instanceId) async {
    final json = await _get('/api/genres/book', instanceId);
    return (json['genres'] as List)
        .map((g) => BookGenre(g['id'] as String, g['name'] as String))
        .toList();
  }

  Future<DiscoveryBook> book(String foreignId, String? instanceId) async {
    if (!DiscoveryBook.validId(foreignId)) {
      throw const FormatException('Invalid book ID');
    }
    final book = DiscoveryBook.fromJson(
        await _get('/api/media/book/${foreignId.substring(3)}', instanceId));
    if (book.foreignId != foreignId) {
      throw const FormatException('Mismatched book identity');
    }
    return book;
  }

  Future<List<BookRequestTarget>> targets(
      String foreignId, String instanceId) async {
    if (!DiscoveryBook.validId(foreignId)) {
      throw const FormatException('Invalid book ID');
    }
    final json = await _get(
        '/api/media/book/${foreignId.substring(3)}/request-target', instanceId);
    return (json['candidates'] as List).map((t) {
      final id = t['foreign_id'] as String;
      final title = t['title'] as String;
      if (id.trim().isEmpty || title.trim().isEmpty) {
        throw const FormatException('Invalid book request target');
      }
      return BookRequestTarget(id, title, t['author'] as String? ?? '');
    }).toList();
  }
}
