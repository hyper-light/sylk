package storage

import "testing"

func TestSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Add user auth", "add_user_auth"},
		{"  Foo  Bar  ", "foo_bar"},
		{"hello-world!", "hello_world"},
		{"  ", "unnamed"},
		{"", "unnamed"},
		{"___", "unnamed"},
		{"Already_snake_case", "already_snake_case"},
		{"MixedCASE123", "mixedcase123"},
		{"a---b___c...d", "a_b_c_d"},
		// Truncation: 60 chars input → capped to 50.
		{"abcdefghij_abcdefghij_abcdefghij_abcdefghij_abcdefghij_abcdefghij", "abcdefghij_abcdefghij_abcdefghij_abcdefghij_abcdef"},
		// Trailing _ after truncation is trimmed.
		{"a_b_c_d_e_f_g_h_i_j_k_l_m_n_o_p_q_r_s_t_u_v_w_x_y_z", "a_b_c_d_e_f_g_h_i_j_k_l_m_n_o_p_q_r_s_t_u_v_w_x_y"},
		// Pure symbols.
		{"@#$%", "unnamed"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Slug(tt.input)
			if got != tt.want {
				t.Errorf("Slug(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
