package lsp

import (
	"testing"
)

func TestExtractExpression(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		col      int
		expected string
	}{
		{
			name:     "module.function at cursor",
			line:     "    Foo.Bar.baz(123)",
			col:      9,
			expected: "Foo.Bar.baz",
		},
		{
			name:     "module name",
			line:     "    Foo.Bar.Baz",
			col:      7,
			expected: "Foo.Bar.Baz",
		},
		{
			name:     "bare function",
			line:     "    do_something(x)",
			col:      7,
			expected: "do_something",
		},
		{
			name:     "cursor at start of expr",
			line:     "    Foo.bar()",
			col:      4,
			expected: "Foo.bar",
		},
		{
			name:     "cursor at end of expr",
			line:     "    Foo.bar()",
			col:      10,
			expected: "Foo.bar",
		},
		{
			name:     "function with question mark",
			line:     "    valid?(x)",
			col:      6,
			expected: "valid?",
		},
		{
			name:     "function with bang",
			line:     "    process!(x)",
			col:      6,
			expected: "process!",
		},
		{
			name:     "module with underscore",
			line:     "    MyApp_Web.Router",
			col:      8,
			expected: "MyApp_Web.Router",
		},
		{
			name:     "empty line",
			line:     "",
			col:      0,
			expected: "",
		},
		{
			name:     "cursor on paren",
			line:     "    Foo.bar()",
			col:      11,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractExpression(tt.line, tt.col)
			if got != tt.expected {
				t.Errorf("ExtractExpression(%q, %d) = %q, want %q", tt.line, tt.col, got, tt.expected)
			}
		})
	}
}

func TestExtractModuleAndFunction(t *testing.T) {
	tests := []struct {
		name         string
		expr         string
		expectedMod  string
		expectedFunc string
	}{
		{
			name:         "module with function",
			expr:         "Foo.Bar.baz",
			expectedMod:  "Foo.Bar",
			expectedFunc: "baz",
		},
		{
			name:         "module without function",
			expr:         "Foo.Bar.Baz",
			expectedMod:  "Foo.Bar.Baz",
			expectedFunc: "",
		},
		{
			name:         "single module",
			expr:         "Repo",
			expectedMod:  "Repo",
			expectedFunc: "",
		},
		{
			name:         "bare function name",
			expr:         "do_something",
			expectedMod:  "",
			expectedFunc: "do_something",
		},
		{
			name:         "function with underscores",
			expr:         "Foo.Bar.my_function_name",
			expectedMod:  "Foo.Bar",
			expectedFunc: "my_function_name",
		},
		{
			name:         "deeply nested module",
			expr:         "MyApp.Handlers.Webhooks.V2.process_event",
			expectedMod:  "MyApp.Handlers.Webhooks.V2",
			expectedFunc: "process_event",
		},
		{
			name:         "empty string",
			expr:         "",
			expectedMod:  "",
			expectedFunc: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mod, fn := ExtractModuleAndFunction(tt.expr)
			if mod != tt.expectedMod {
				t.Errorf("module: got %q, want %q", mod, tt.expectedMod)
			}
			if fn != tt.expectedFunc {
				t.Errorf("function: got %q, want %q", fn, tt.expectedFunc)
			}
		})
	}
}

func TestExtractAliases(t *testing.T) {
	t.Run("simple alias", func(t *testing.T) {
		aliases := ExtractAliases("  alias MyApp.Repo")
		if aliases["Repo"] != "MyApp.Repo" {
			t.Errorf("got %q, want MyApp.Repo", aliases["Repo"])
		}
	})

	t.Run("alias with as:", func(t *testing.T) {
		aliases := ExtractAliases("  alias MyApp.Handlers.Foo, as: MyFoo")
		if aliases["MyFoo"] != "MyApp.Handlers.Foo" {
			t.Errorf("got %q, want MyApp.Handlers.Foo", aliases["MyFoo"])
		}
	})

	t.Run("multi-alias", func(t *testing.T) {
		aliases := ExtractAliases("  alias MyApp.Handlers.{Foo, Bar, Baz}")
		if aliases["Foo"] != "MyApp.Handlers.Foo" {
			t.Errorf("Foo: got %q", aliases["Foo"])
		}
		if aliases["Bar"] != "MyApp.Handlers.Bar" {
			t.Errorf("Bar: got %q", aliases["Bar"])
		}
		if aliases["Baz"] != "MyApp.Handlers.Baz" {
			t.Errorf("Baz: got %q", aliases["Baz"])
		}
	})

	t.Run("multiple alias lines", func(t *testing.T) {
		text := "  alias MyApp.Repo\n  alias MyApp.Accounts.User\n  alias MyApp.Handlers.{Foo, Bar}"
		aliases := ExtractAliases(text)
		if aliases["Repo"] != "MyApp.Repo" {
			t.Errorf("Repo: got %q", aliases["Repo"])
		}
		if aliases["User"] != "MyApp.Accounts.User" {
			t.Errorf("User: got %q", aliases["User"])
		}
		if aliases["Foo"] != "MyApp.Handlers.Foo" {
			t.Errorf("Foo: got %q", aliases["Foo"])
		}
		if aliases["Bar"] != "MyApp.Handlers.Bar" {
			t.Errorf("Bar: got %q", aliases["Bar"])
		}
	})

	t.Run("ignores non-alias lines", func(t *testing.T) {
		text := "defmodule Foo do\n  use GenServer\n  alias MyApp.Repo\n  def foo, do: :ok"
		aliases := ExtractAliases(text)
		if len(aliases) != 1 {
			t.Errorf("expected 1 alias, got %d", len(aliases))
		}
		if aliases["Repo"] != "MyApp.Repo" {
			t.Errorf("Repo: got %q", aliases["Repo"])
		}
	})
}

func TestExtractImports(t *testing.T) {
	t.Run("parses imports", func(t *testing.T) {
		text := "  import MyApp.Helpers.Formatting\n  import Ecto.Query"
		imports := ExtractImports(text)
		if len(imports) != 2 {
			t.Fatalf("expected 2 imports, got %d", len(imports))
		}
		if imports[0] != "MyApp.Helpers.Formatting" {
			t.Errorf("imports[0]: got %q", imports[0])
		}
		if imports[1] != "Ecto.Query" {
			t.Errorf("imports[1]: got %q", imports[1])
		}
	})

	t.Run("ignores non-import lines", func(t *testing.T) {
		text := "defmodule Foo do\n  import Ecto.Query\n  alias MyApp.Repo"
		imports := ExtractImports(text)
		if len(imports) != 1 {
			t.Errorf("expected 1 import, got %d", len(imports))
		}
	})
}

func TestFindFunctionDefinition(t *testing.T) {
	text := `defmodule Foo do
  def public_func(a, b) do
    a + b
  end

  defp private_func(x) do
    x * 2
  end

  defmacro my_macro(expr) do
    quote do: unquote(expr)
  end

  defmacrop private_macro(expr) do
    quote do: unquote(expr)
  end
end`

	tests := []struct {
		name          string
		functionName  string
		expectedLine  int
		expectedFound bool
	}{
		{"public function", "public_func", 2, true},
		{"private function", "private_func", 6, true},
		{"macro", "my_macro", 10, true},
		{"private macro", "private_macro", 14, true},
		{"missing function", "nonexistent", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, found := FindFunctionDefinition(text, tt.functionName)
			if found != tt.expectedFound {
				t.Errorf("found: got %v, want %v", found, tt.expectedFound)
			}
			if line != tt.expectedLine {
				t.Errorf("line: got %d, want %d", line, tt.expectedLine)
			}
		})
	}
}

func TestFindFunctionDefinition_Guards(t *testing.T) {
	text := `defmodule Foo do
  defguard is_admin(user) when user.role == :admin
  defguardp is_active(user) when user.status == :active
end`

	line, found := FindFunctionDefinition(text, "is_admin")
	if !found || line != 2 {
		t.Errorf("is_admin: got line %d found %v", line, found)
	}

	line, found = FindFunctionDefinition(text, "is_active")
	if !found || line != 3 {
		t.Errorf("is_active: got line %d found %v", line, found)
	}
}

func TestFindFunctionDefinition_Delegate(t *testing.T) {
	text := `defmodule Foo do
  defdelegate fetch(id), to: MyApp.Repo
end`

	line, found := FindFunctionDefinition(text, "fetch")
	if !found || line != 2 {
		t.Errorf("fetch: got line %d found %v", line, found)
	}
}

func TestFindFunctionDefinition_InlineDo(t *testing.T) {
	text := `defmodule Foo do
  def add(a, b), do: a + b
  defp secret(x), do: x * 2
end`

	line, found := FindFunctionDefinition(text, "add")
	if !found || line != 2 {
		t.Errorf("add: got line %d found %v", line, found)
	}
	line, found = FindFunctionDefinition(text, "secret")
	if !found || line != 3 {
		t.Errorf("secret: got line %d found %v", line, found)
	}
}

func TestExtractExpression_PipeOperator(t *testing.T) {
	line := "    |> Foo.Bar.transform()"
	got := ExtractExpression(line, 12)
	if got != "Foo.Bar.transform" {
		t.Errorf("got %q, want Foo.Bar.transform", got)
	}
}

func TestExtractAliases_DoesNotMatchAliasInStrings(t *testing.T) {
	// Lines that happen to contain "alias" but aren't real alias declarations
	text := `  some_var = "alias MyApp.Fake"
  alias MyApp.Real`
	aliases := ExtractAliases(text)
	if _, ok := aliases["Fake"]; ok {
		t.Error("should not match alias inside a string")
	}
	if aliases["Real"] != "MyApp.Real" {
		t.Errorf("Real: got %q", aliases["Real"])
	}
}

func TestExtractModuleAndFunction_QuestionMarkBang(t *testing.T) {
	mod, fn := ExtractModuleAndFunction("Foo.valid?")
	if mod != "Foo" || fn != "valid?" {
		t.Errorf("got mod=%q fn=%q", mod, fn)
	}

	mod, fn = ExtractModuleAndFunction("Foo.process!")
	if mod != "Foo" || fn != "process!" {
		t.Errorf("got mod=%q fn=%q", mod, fn)
	}
}
