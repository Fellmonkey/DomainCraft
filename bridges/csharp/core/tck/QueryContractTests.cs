/* Hand-written oracle against kitchen-sink.yaml: the path:op:value grammar
 * (spec/query.md) in C# — FilterParser, FilterExpression, SortParser/SortApplier,
 * ListQuery normalization and PagedResult math. Perm-entity maps below mirror
 * what the IR emits as FilterablePaths()/SortableFields(). */
using System.Text.Json;
using TestPlatform.Application.Query;
using TestPlatform.Domain.Entities;
using TestPlatform.Domain.Shared;
using Xunit;

namespace DomainCraft.Core.Tck;

public class QueryContractTests
{
    // Per-entity schemas mirroring kitchen-sink.yaml FilterablePaths() output.
    private static readonly Dictionary<string, FilterOperator[]> OrderAllowed = new()
    {
        ["orderNumber"] = new[] { FilterOperator.Eq, FilterOperator.Ne, FilterOperator.Contains, FilterOperator.StartsWith, FilterOperator.EndsWith, FilterOperator.In },
        ["status"] = new[] { FilterOperator.Eq, FilterOperator.Ne, FilterOperator.In },
        ["totalAmount"] = new[] { FilterOperator.Eq, FilterOperator.Ne, FilterOperator.Gt, FilterOperator.Gte, FilterOperator.Lt, FilterOperator.Lte, FilterOperator.In },
        ["user.firstName"] = new[] { FilterOperator.Eq, FilterOperator.Ne, FilterOperator.Contains, FilterOperator.StartsWith, FilterOperator.EndsWith, FilterOperator.In },
    };

    // plain `json` → text operators only; `jsonb` → full set incl. ordered
    private static readonly Dictionary<string, FilterOperator[]> OrderJsonRoots = new()
    {
        ["shippingAddress"] = new[] { FilterOperator.Eq, FilterOperator.Ne, FilterOperator.Contains, FilterOperator.StartsWith, FilterOperator.EndsWith, FilterOperator.In },
    };

    private static readonly Dictionary<string, FilterOperator[]> DocumentJsonRoots = new()
    {
        ["metadata"] = new[] { FilterOperator.Eq, FilterOperator.Ne, FilterOperator.Gt, FilterOperator.Gte, FilterOperator.Lt, FilterOperator.Lte, FilterOperator.Contains, FilterOperator.StartsWith, FilterOperator.EndsWith, FilterOperator.In },
    };

    // ===== FilterOperator vocabulary =====

    [Fact]
    public void FilterOperator_vocabulary_is_canonical_and_not_drifted()
    {
        // Generated from IRProject.AllFilterOperators — a hand-maintained
        // parallel grammar would fail here.
        var names = Enum.GetNames<FilterOperator>();
        Assert.Equal(
            new[] { "Eq", "Ne", "Gt", "Gte", "Lt", "Lte", "In", "Contains", "StartsWith", "EndsWith" },
            names);
    }

    // ===== FilterParser: path:op:value grammar =====

    [Fact]
    public void Parses_a_bare_compare_term()
    {
        var node = FilterParser.Parse("status:eq:pending", OrderAllowed);
        Assert.NotNull(node);
        Assert.Equal("compare", node!.Kind);
        Assert.Equal("status", node.Field);
        Assert.Equal(FilterOperator.Eq, node.Op);
        Assert.Equal(new[] { "pending" }, node.Values);
    }

    [Fact]
    public void Parses_and_terms_separated_by_commas()
    {
        var node = FilterParser.Parse("totalAmount:gte:100,totalAmount:lt:500", OrderAllowed);
        Assert.NotNull(node);
        Assert.Equal("and", node!.Kind);
        Assert.Equal(2, node.Children.Count);
        Assert.All(node.Children, c => Assert.Equal("compare", c.Kind));
    }

    [Fact]
    public void Parses_or_terms_separated_by_pipes()
    {
        var node = FilterParser.Parse("status:eq:pending|status:eq:shipped", OrderAllowed);
        Assert.NotNull(node);
        Assert.Equal("or", node!.Kind);
        Assert.Equal(2, node.Children.Count);
    }

    [Fact]
    public void Parses_in_as_a_parenthesized_list()
    {
        var node = FilterParser.Parse("status:in:(pending,shipped)", OrderAllowed);
        Assert.NotNull(node);
        Assert.Equal(FilterOperator.In, node!.Op);
        Assert.Equal(new[] { "pending", "shipped" }, node.Values);
    }

    [Fact]
    public void JSON_roots_resolve_through_the_root_operator_set()
    {
        // json (plain) root: ordered ops rejected → FormatException.
        Assert.Throws<FormatException>(() =>
            FilterParser.Parse("shippingAddress.zip:gte:1000", OrderAllowed, OrderJsonRoots));
        // Text ops on the json root pass.
        Assert.NotNull(FilterParser.Parse("shippingAddress.city:eq:Berlin", OrderAllowed, OrderJsonRoots));
        // jsonb root: ordered ops allowed.
        var node = FilterParser.Parse("metadata.score:gte:10", DocumentJsonRoots, DocumentJsonRoots);
        Assert.NotNull(node);
        Assert.Equal(FilterOperator.Gte, node!.Op);
    }

    [Theory]
    [InlineData("status:eq")]  // missing value
    [InlineData("status:eq:pending,unknownField:eq:1")]  // unknown field
    [InlineData("status:gte:1")]  // ordered op on enum
    [InlineData("totalAmount:contains:5")]  // text op on numeric
    [InlineData("nope")]  // not path:op:value
    [InlineData("status:in:pending,shipped")]  // in without parens
    public void Invalid_filters_are_rejected(string input)
    {
        Assert.Throws<FormatException>(() => FilterParser.Parse(input, OrderAllowed, OrderJsonRoots));
    }

    // ===== FilterExpression: LINQ translation (in-memory oracle) =====

    private static List<Order> SampleOrders()
    {
        return new List<Order>
        {
            new() { OrderNumber = "ORD-001", Status = OrderStatus.Pending, TotalAmount = 50m, Subtotal = 50m, User = new User { FirstName = "Ana", Email = "ana@test.com" } },
            new() { OrderNumber = "ORD-002", Status = OrderStatus.Shipped, TotalAmount = 120m, Subtotal = 120m, User = new User { FirstName = "Bob", Email = "bob@test.com" } },
            new() { OrderNumber = "ORD-003", Status = OrderStatus.Shipped, TotalAmount = 300m, Subtotal = 300m, User = new User { FirstName = "Ana", Email = "ana2@test.com" } },
        };
    }

    [Fact]
    public void Numeric_gte_filters_in_memory()
    {
        var expr = FilterExpression.Build<Order>(FilterParser.Parse("totalAmount:gte:100", OrderAllowed)!);
        Assert.Equal(new[] { "ORD-002", "ORD-003" }, SampleOrders().Where(expr.Compile()).Select(o => o.OrderNumber));
    }

    [Fact]
    public void Enum_wire_value_filters_match_the_enum_member()
    {
        var expr = FilterExpression.Build<Order>(FilterParser.Parse("status:eq:shipped", OrderAllowed)!);
        Assert.Equal(new[] { "ORD-002", "ORD-003" }, SampleOrders().Where(expr.Compile()).Select(o => o.OrderNumber));
    }

    [Fact]
    public void In_with_wire_values_matches_multiple()
    {
        var expr = FilterExpression.Build<Order>(FilterParser.Parse("status:in:(pending,shipped)", OrderAllowed)!);
        Assert.Equal(3, SampleOrders().Where(expr.Compile()).Count());
    }

    [Fact]
    public void One_hop_relation_paths_are_walked()
    {
        var expr = FilterExpression.Build<Order>(FilterParser.Parse("user.firstName:contains:An", OrderAllowed)!);
        Assert.Equal(new[] { "ORD-001", "ORD-003" }, SampleOrders().Where(expr.Compile()).Select(o => o.OrderNumber));
    }

    [Fact]
    public void Json_text_root_compares_strings()
    {
        var node = FilterParser.Parse("shippingAddress.city:eq:Berlin", OrderAllowed, OrderJsonRoots)!;
        var expr = FilterExpression.Build<Order>(node);

        var orders = new List<Order>
        {
            new() { OrderNumber = "J1", TotalAmount = 1m, Subtotal = 1m, ShippingAddress = JsonDocument.Parse("""{"city":"Berlin"}""") },
            new() { OrderNumber = "J2", TotalAmount = 1m, Subtotal = 1m, ShippingAddress = JsonDocument.Parse("""{"city":"Paris"}""") },
        };
        Assert.Equal(new[] { "J1" }, orders.Where(expr.Compile()).Select(o => o.OrderNumber));
    }

    [Fact]
    public void Jsonb_root_ordered_operators_compare_numerics()
    {
        var node = FilterParser.Parse("metadata.score:gte:10", DocumentJsonRoots, DocumentJsonRoots)!;
        var expr = FilterExpression.Build<Document>(node);

        var docs = new List<Document>
        {
            new() { Title = "low", Metadata = JsonDocument.Parse("""{"score":5}""") },
            new() { Title = "high", Metadata = JsonDocument.Parse("""{"score":15.5}""") },
        };
        Assert.Equal(new[] { "high" }, docs.Where(expr.Compile()).Select(d => d.Title));
    }

    // ===== Sort grammar =====

    private static readonly Dictionary<string, string> OrderSortable = new()
    {
        ["orderNumber"] = "OrderNumber",
        ["totalAmount"] = "TotalAmount",
    };

    [Fact]
    public void Sort_parser_resolves_canonical_names_and_direction()
    {
        var keys = SortParser.Parse("-totalAmount,orderNumber", OrderSortable);
        Assert.Equal(2, keys.Count);
        Assert.Equal("TotalAmount", keys[0].Property);
        Assert.True(keys[0].Descending);
        Assert.Equal("OrderNumber", keys[1].Property);
        Assert.False(keys[1].Descending);
    }

    [Theory]
    [InlineData("-notAField")]
    [InlineData("totalAmount,")]
    [InlineData("-totalAmount,notAField")]
    public void Invalid_sort_keys_are_rejected(string input)
    {
        Assert.Throws<FormatException>(() => SortParser.Parse(input, OrderSortable));
    }

    [Fact]
    public void Empty_sort_input_returns_no_keys()
    {
        Assert.Empty(SortParser.Parse("", OrderSortable));
        Assert.Empty(SortParser.Parse(null, OrderSortable));
        Assert.Empty(SortParser.Parse("   ", OrderSortable));
    }

    [Fact]
    public void Sort_applier_orders_in_memory()
    {
        var keys = SortParser.Parse("-totalAmount", OrderSortable);
        var ordered = SortApplier.Apply(SampleOrders().AsQueryable(), keys).ToList();
        Assert.Equal(new[] { 300m, 120m, 50m }, ordered.Select(o => o.TotalAmount));
    }

    // ===== ListQuery normalization (transport → repository) =====

    [Fact]
    public void FromQuery_fills_defaults_and_clamps()
    {
        var q = ListQuery.FromQuery(page: 0, pageSize: null, limit: null, sort: null, search: null, filter: null, cursor: null, include: null, defaultPageSize: 20, maxPageSize: 200);
        Assert.Equal(1, q.Page);
        Assert.Equal(20, q.PageSize);

        var clamped = ListQuery.FromQuery(page: 3, pageSize: 500, limit: null, sort: null, search: null, filter: null, cursor: null, include: null, defaultPageSize: 20, maxPageSize: 200);
        Assert.Equal(200, clamped.PageSize);
    }

    [Fact]
    public void Limit_wins_over_pageSize()
    {
        var q = ListQuery.FromQuery(page: 1, pageSize: 5, limit: 20, sort: null, search: null, filter: null, cursor: null, include: null, defaultPageSize: 20, maxPageSize: 200);
        Assert.Equal(20, q.PageSize);
    }

    [Fact]
    public void Include_is_an_optional_restrict_list()
    {
        var everything = ListQuery.FromQuery(1, null, null, null, null, null, null, include: null, 20, 200);
        Assert.True(everything.ShouldInclude("items"));
        Assert.True(everything.ShouldInclude("payments"));

        var restricted = ListQuery.FromQuery(1, null, null, null, null, null, null, include: "items", 20, 200);
        Assert.True(restricted.ShouldInclude("items"));
        Assert.False(restricted.ShouldInclude("payments"));
    }

    // ===== PagedResult math =====

    [Fact]
    public void PagedResult_computes_page_metadata()
    {
        // 41 rows at 20/page → 3 pages; page 2 has a next, page 3 does not.
        var page = new PagedResult<Order> { Items = new List<Order> { new() }, TotalCount = 41, Page = 2, PageSize = 20 };
        Assert.Equal(3, page.TotalPages);
        Assert.True(page.HasNextPage);
        Assert.True(page.HasPreviousPage);
        Assert.False(new PagedResult<Order> { TotalCount = 41, Page = 3, PageSize = 20 }.HasNextPage);
        Assert.False(new PagedResult<Order> { TotalCount = 20, Page = 1, PageSize = 20 }.HasNextPage);
        Assert.False(new PagedResult<Order> { Page = 1 }.HasPreviousPage);
    }
}