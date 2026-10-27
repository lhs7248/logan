package com.meituan.logan.web.mapper;

import org.apache.ibatis.builder.xml.XMLMapperBuilder;
import org.apache.ibatis.mapping.Environment;
import org.apache.ibatis.session.Configuration;
import org.apache.ibatis.session.SqlSession;
import org.apache.ibatis.session.SqlSessionFactoryBuilder;
import org.apache.ibatis.transaction.jdbc.JdbcTransactionFactory;
import org.apache.ibatis.datasource.unpooled.UnpooledDataSource;
import org.junit.Test;

import java.io.InputStream;
import java.sql.Statement;

import static org.junit.Assert.assertEquals;
import static org.junit.Assume.assumeNotNull;

/** Real MySQL regression; the session-local temporary table shadows web_task. */
public class WebLogTaskMapperTest {
    @Test
    public void maxIdSupportsEmptyAndPopulatedTables() throws Exception {
        String url = System.getenv("LOGAN_TEST_JDBC_URL");
        assumeNotNull(url);
        UnpooledDataSource dataSource = new UnpooledDataSource(
                "com.mysql.jdbc.Driver", url,
                System.getenv("LOGAN_TEST_DB_USER"), System.getenv("LOGAN_TEST_DB_PASSWORD"));
        Configuration config = new Configuration(new Environment("test",
                new JdbcTransactionFactory(), dataSource));
        String resource = "sqlmap/WebLogTaskMapper.xml";
        try (InputStream xml = getClass().getClassLoader().getResourceAsStream(resource)) {
            new XMLMapperBuilder(xml, config, resource, config.getSqlFragments()).parse();
        }
        try (SqlSession session = new SqlSessionFactoryBuilder().build(config).openSession();
             Statement statement = session.getConnection().createStatement()) {
            statement.execute("CREATE TEMPORARY TABLE web_task (id BIGINT UNSIGNED PRIMARY KEY)");
            WebLogTaskMapper mapper = session.getMapper(WebLogTaskMapper.class);
            assertEquals(0L, mapper.maxId());
            statement.execute("INSERT INTO web_task (id) VALUES (2), (17), (5)");
            session.clearCache();
            assertEquals(17L, mapper.maxId());
        }
    }
}
