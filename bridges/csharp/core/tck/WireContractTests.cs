/* Hand-written oracle against kitchen-sink.yaml: certifies the thin
 * csharp-core layer only (Entities/Enums/Query). Compiling the generated
 * sources into this project is itself a contract check — a template that
 * stops emitting valid C# fails `dotnet build` before any assertion runs. */
using System.ComponentModel.DataAnnotations;
using System.ComponentModel.DataAnnotations.Schema;
using System.Reflection;
using System.Runtime.Serialization;
using System.Text.Json;
using System.Text.Json.Serialization;
using TestPlatform.Domain.Entities;
using TestPlatform.Domain.Shared;
using Xunit;

namespace DomainCraft.Core.Tck;

public class WireContractTests
{
    // ===== 1. Enum wire values are core-owned snake_case ([EnumMember]) =====

    [Theory]
    [InlineData(OrderStatus.Pending, "pending")]
    [InlineData(OrderStatus.Processing, "processing")]
    [InlineData(OrderStatus.Shipped, "shipped")]
    [InlineData(OrderStatus.Delivered, "delivered")]
    [InlineData(OrderStatus.Cancelled, "cancelled")]
    public void OrderStatus_wire_values_are_snake_case(OrderStatus member, string wire)
    {
        Assert.Equal(wire, Wire(member));
    }

    [Theory]
    [InlineData(DocumentStatus.Draft, "draft")]
    [InlineData(DocumentStatus.Review, "review")]
    [InlineData(DocumentStatus.Published, "published")]
    [InlineData(DocumentStatus.Archived, "archived")]
    public void DocumentStatus_wire_values_are_snake_case(DocumentStatus member, string wire)
    {
        Assert.Equal(wire, Wire(member));
    }

    [Theory]
    [InlineData(PaymentMethod.CreditCard, "credit_card")]
    [InlineData(PaymentMethod.DebitCard, "debit_card")]
    [InlineData(PaymentMethod.PayPal, "pay_pal")]
    [InlineData(PaymentMethod.BankTransfer, "bank_transfer")]
    [InlineData(PaymentMethod.Crypto, "crypto")]
    public void PaymentMethod_wire_values_are_snake_case(PaymentMethod member, string wire)
    {
        Assert.Equal(wire, Wire(member));
    }

    [Theory]
    [InlineData(ProductStatus.Draft, "draft")]
    [InlineData(ProductStatus.Published, "published")]
    [InlineData(ProductStatus.Archived, "archived")]
    public void ProductStatus_wire_values_are_snake_case(ProductStatus member, string wire)
    {
        Assert.Equal(wire, Wire(member));
    }

    [Theory]
    [InlineData(UserRole.Admin, "admin")]
    [InlineData(UserRole.Manager, "manager")]
    [InlineData(UserRole.Editor, "editor")]
    [InlineData(UserRole.User, "user")]
    [InlineData(UserRole.Viewer, "viewer")]
    public void UserRole_wire_values_are_snake_case(UserRole member, string wire)
    {
        Assert.Equal(wire, Wire(member));
    }

    // ===== 2. JSON converter: write emits the wire value, read accepts it =====

    private static JsonSerializerOptions ConverterOptions()
    {
        var options = new JsonSerializerOptions(JsonSerializerDefaults.Web);
        options.Converters.Add(new OrderStatusJsonConverter());
        options.Converters.Add(new PaymentMethodJsonConverter());
        return options;
    }

    [Fact]
    public void Json_converter_emits_the_wire_value_not_the_member_name()
    {
        // A naive JsonStringEnumConverter would leak "PayPal" over the wire.
        Assert.Equal("\"pay_pal\"", JsonSerializer.Serialize(PaymentMethod.PayPal, ConverterOptions()));
        Assert.Equal("\"processing\"", JsonSerializer.Serialize(OrderStatus.Processing, ConverterOptions()));
    }

    [Fact]
    public void Json_converter_accepts_wire_and_member_names_case_insensitively()
    {
        var options = ConverterOptions();
        Assert.Equal(OrderStatus.Shipped, JsonSerializer.Deserialize<OrderStatus>("\"shipped\"", options));
        Assert.Equal(PaymentMethod.PayPal, JsonSerializer.Deserialize<PaymentMethod>("\"paypal\"", options)); // lowercase member fallback
    }

    [Fact]
    public void Json_converter_rejects_unknown_wire_values()
    {
        Assert.Throws<JsonException>(() =>
            JsonSerializer.Deserialize<OrderStatus>("\"nope\"", ConverterOptions()));
    }

    // ===== 3. Entity type surface =====

    [Fact]
    public void Entity_primary_keys_follow_the_model_type()
    {
        // uuid PK → Guid; int PK → int (drives keyset pagination).
        Assert.Equal(typeof(Guid), Prop<Order>(nameof(Order.Id)).PropertyType);
        Assert.Equal(typeof(int), Prop<Counter>(nameof(Counter.Id)).PropertyType);
        Assert.NotNull(Prop<Counter>(nameof(Counter.Id)).GetCustomAttribute<KeyAttribute>());
    }

    [Fact]
    public void Scalar_types_follow_the_core_type_mapping()
    {
        Assert.Equal(typeof(decimal), Prop<Order>(nameof(Order.TotalAmount)).PropertyType);
        Assert.Equal(typeof(string), Prop<Order>(nameof(Order.OrderNumber)).PropertyType);
        Assert.Equal(typeof(long), Prop<User>(nameof(User.LoginCount)).PropertyType);
        Assert.Equal(typeof(double?), Prop<User>(nameof(User.Rating)).PropertyType); // float → double, optional → nullable
        Assert.Equal(typeof(bool), Prop<User>(nameof(User.IsActive)).PropertyType);
        Assert.Equal(typeof(OrderStatus), Prop<Order>(nameof(Order.Status)).PropertyType);
        Assert.Equal(typeof(UserRole), Prop<User>(nameof(User.Role)).PropertyType);
        Assert.Equal(typeof(UserRole), Prop<User>(nameof(User.Role)).PropertyType);
    }

    [Fact]
    public void Nullable_value_types_are_T_null_in_CLR()
    {
        Assert.Equal(typeof(decimal?), Prop<User>(nameof(User.Balance)).PropertyType);
        Assert.Equal(typeof(double?), Prop<User>(nameof(User.Rating)).PropertyType);
        Assert.Equal(typeof(Guid), Prop<Order>(nameof(Order.UserId)).PropertyType); // required FK
        Assert.Equal(typeof(Guid?), Prop<User>(nameof(User.TenantId)).PropertyType);
        Assert.Equal(typeof(DateTime?), Prop<User>(nameof(User.LastLoginAt)).PropertyType);
        Assert.Equal(typeof(JsonDocument), Prop<User>(nameof(User.Metadata)).PropertyType); // jsonb, optional by annotation
    }

    [Fact]
    public void Nullability_annotations_distinguish_optional_and_required_references()
    {
        // The generated module does not enable #nullable (CS8669 is suppressed
        // by design), so metadata-based nullability is unavailable — assert the
        // emitted source shape instead: optional references get `?`, required
        // ones stay plain.
        var user = File.ReadAllText(GeneratedPath("User.g.cs"));
        Assert.Matches(@"public string\? Bio", user);
        Assert.Matches(@"public string Email", user);

        var order = File.ReadAllText(GeneratedPath("Order.g.cs"));
        Assert.Matches(@"public string\? Notes", order);
        Assert.Matches(@"public string OrderNumber", order);
        Assert.Matches(@"public JsonDocument\? ShippingAddress", order);
    }

    [Fact]
    public void Relations_map_to_fk_column_plus_navigation()
    {
        Assert.Equal(typeof(Guid), Prop<Order>(nameof(Order.UserId)).PropertyType);
        var nav = Prop<Order>("User");
        Assert.NotNull(nav);
        Assert.Equal(typeof(User), nav.PropertyType);
        Assert.NotNull(nav.GetCustomAttribute<JsonIgnoreAttribute>());
    }

    [Fact]
    public void Many_relations_are_typed_collections()
    {
        Assert.Equal(typeof(ICollection<OrderItem>), Prop<Order>(nameof(Order.Items)).PropertyType);
        Assert.Equal(typeof(ICollection<Payment>), Prop<Order>(nameof(Order.Payments)).PropertyType);
    }

    [Fact]
    public void Arrays_are_List_of_the_element_type()
    {
        Assert.Equal(typeof(List<string>), Prop<User>(nameof(User.PhoneNumbers)).PropertyType);
        Assert.Equal(typeof(List<Guid>), Prop<User>(nameof(User.Tags)).PropertyType);
        Assert.Equal(typeof(List<DateTime>), Prop<User>(nameof(User.FavoriteDates)).PropertyType);
        Assert.Equal(typeof(List<string>), Prop<Order>(nameof(Order.TrackingNumbers)).PropertyType);
        Assert.Equal(typeof(List<PaymentMethod>), Prop<Product>(nameof(Product.SupportedPaymentMethods)).PropertyType);
    }

    [Fact]
    public void Hidden_and_password_fields_are_json_ignored()
    {
        Assert.NotNull(Prop<User>(nameof(User.Password)).GetCustomAttribute<JsonIgnoreAttribute>());
        Assert.NotNull(Prop<Document>(nameof(Document.InternalNotes)).GetCustomAttribute<JsonIgnoreAttribute>());
        Assert.NotNull(Prop<Product>(nameof(Product.InternalNotes)).GetCustomAttribute<JsonIgnoreAttribute>());
    }

    [Fact]
    public void Feature_fields_are_injected_by_the_model_features()
    {
        // audit → CreatedAt/UpdatedAt, audit_log → CreatedBy/UpdatedBy,
        // soft_delete → DeletedAt, optimistic_lock → Version.
        Assert.NotNull(Prop<User>("CreatedAt"));
        Assert.NotNull(Prop<User>("UpdatedAt"));
        Assert.NotNull(Prop<User>("CreatedBy"));
        Assert.NotNull(Prop<User>("UpdatedBy"));
        Assert.Equal(typeof(DateTime?), Prop<User>("DeletedAt").PropertyType);
        Assert.Equal(typeof(int), Prop<Order>("Version").PropertyType);
        Assert.NotNull(Prop<Order>("Version").GetCustomAttribute<ConcurrencyCheckAttribute>());
        // Counter declares no features — none injected.
        Assert.Null(typeof(Counter).GetProperty("Version", BindingFlags.Public | BindingFlags.Instance));
    }

    private static string Wire<TEnum>(TEnum value) where TEnum : struct, Enum
    {
        var name = Enum.GetName(typeof(TEnum), value)!;
        var attr = typeof(TEnum).GetMember(name)[0].GetCustomAttribute<EnumMemberAttribute>();
        Assert.NotNull(attr);
        return attr.Value!;
    }

    private static PropertyInfo Prop<T>(string name) =>
        typeof(T).GetProperty(name, BindingFlags.Public | BindingFlags.Instance)
        ?? throw new Xunit.Sdk.XunitException($"Property '{name}' missing on {typeof(T).Name} — the template stopped emitting it.");

    // The tests run from bin/Debug/net10.0 — walk up to the generated/ dir.
    private static string GeneratedPath(string entityFile)
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir != null && !Directory.Exists(Path.Combine(dir.FullName, "generated")))
            dir = dir.Parent;
        Assert.NotNull(dir);
        return Path.Combine(dir.FullName, "generated", "src", "Domain", "Entities", entityFile);
    }
}